// Package llamacpp provides a provider implementation for llama.cpp's HTTP server.
// It implements the LLM and Embedder interfaces with explicit support for
// KV-cache prompt caching optimization.
package llamacpp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/norman/norman/pkg/config"
	"github.com/norman/norman/pkg/llm/api"
)

// --- Request / Response Types ---

// ChatRequest mirrors the llama.cpp native /completion endpoint body.
type ChatRequest struct {
	Prompt      string   `json:"prompt"`
	NPredict    int      `json:"n_predict,omitempty"`
	Model       string   `json:"model"`
	Stream      bool     `json:"stream"`
	Temperature float64  `json:"temperature,omitempty"`
	Stop        []string `json:"stop,omitempty"`
	CachePrompt bool     `json:"cache_prompt,omitempty"`
}

// ChatResponse is the llama.cpp native /completion response.
type ChatResponse struct {
	Content    string `json:"content"`
	StopReason string `json:"stop_reason,omitempty"`
	RawJSON    []byte `json:"-"`
}

// EmbedRequest is the body for /api/v1/embeddings (OpenAI-compatible).
type EmbedRequest struct {
	Input    string `json:"input"`
	Model    string `json:"model,omitempty"`
	Truncate bool   `json:"truncate,omitempty"`
}

// EmbedResponse is the OpenAI-compatible embedding result (from /api/v1/embeddings).
type EmbedResponse struct {
	Data  []EmbedData `json:"data"`
	Usage Usage       `json:"usage"`
}

// NativeEmbedResponse is the llama.cpp-native /embeddings response shape.
// Returns a single pooled embedding vector with token metadata.
type NativeEmbedResponse struct {
	Embedding []float32 `json:"embedding"`
	Tokens    []int     `json:"tokens,omitempty"`
	Pooling   string    `json:"pooling,omitempty"`
}

// EmbedData holds a single embedding vector (OpenAI-compatible format).
type EmbedData struct {
	Object    string    `json:"object"`
	Index     int       `json:"index"`
	Embedding []float32 `json:"embedding"`
}

// Usage matches llama.cpp's usage reporting for embeddings.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CachedTokens     int `json:"cached_tokens,omitempty"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// --- Provider Implementation ---

// Provider implements api.LLM + api.Embedder for llama.cpp's HTTP server.
type Provider struct {
	BaseURL           string
	Model             string
	Temperature       float64
	MaxTokens         int
	HTTPClient        *http.Client
	Timeout           time.Duration
	CacheEnabled      bool   // whether --cache-prompt is active on the server
	CacheSize         int    // max KV cache tokens (n_ctx)
	CacheType         string // "fq" or quantized type
	EmbeddingEndpoint string // embedding endpoint path; "" = auto-detect, "/api/v1/embeddings", "/embeddings"
	providerName      string // configured provider name (e.g. "local-llamacpp")
}

// NewProvider creates a provider configured from workspace config and
// optional llama.cpp-specific settings. The name is required — a provider
// cannot exist without one.
func NewProvider(name string, cfg *config.WorkspaceConfig, llmCfg *Config, opts ...Option) *Provider {
	p := &Provider{
		BaseURL:      llmCfg.BaseURL,
		Model:        cfg.Model,
		Temperature:  cfg.Temperature,
		MaxTokens:    cfg.MaxTokens,
		HTTPClient:   &http.Client{Timeout: 120 * time.Second},
		Timeout:      120 * time.Second,
		CacheEnabled: llmCfg.CacheEnabled,
		CacheSize:    llmCfg.CacheSize,
		CacheType:    llmCfg.CacheType,
		providerName: name,
	}

	for _, opt := range opts {
		opt(p)
	}

	return p
}

// Complete sends a chat completion request and returns the full response.
func (p *Provider) Complete(ctx context.Context, req api.CompletionRequest) (*api.CompletionResponse, error) {
	if p.HTTPClient == nil {
		p.HTTPClient = &http.Client{Timeout: p.Timeout}
	}

	model := req.Model
	if model == "" {
		model = p.Model
	}

	body := ChatRequest{
		Prompt:      buildPrompt(req.Messages),
		NPredict:    req.MaxTokens,
		Model:       model,
		Temperature: req.Temperature,
		Stop:        req.StopSequences,
		CachePrompt: p.CacheEnabled,
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request body: %w", err)
	}

	url := p.BaseURL + "/completion"

	reqHTTP, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(payload))
	if err != nil {
		return nil, wrapError(api.ErrProviderDown, fmt.Errorf("create request: %w", err))
	}

	reqHTTP.Header.Set("Content-Type", "application/json")

	resp, err := p.HTTPClient.Do(reqHTTP)
	if err != nil {
		return nil, wrapError(api.ErrProviderDown, fmt.Errorf("HTTP request failed: %w", err))
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, wrapError(api.ErrProviderDown, fmt.Errorf("read response body: %w", err))
	}

	p.Logf("Complete raw response body: %s", string(bodyBytes))

	if resp.StatusCode != http.StatusOK {
		return nil, mapHTTPError(resp.StatusCode, string(bodyBytes))
	}

	var chatResp ChatResponse
	if err := json.Unmarshal(bodyBytes, &chatResp); err != nil {
		return nil, wrapError(api.ErrProviderDown, fmt.Errorf("parse response: %w", err))
	}

	completion := &api.CompletionResponse{
		ID:           "", // native API doesn't return an ID
		Model:        req.Model,
		Message:      api.Message{Role: "assistant", Content: chatResp.Content},
		FinishReason: chatResp.StopReason,
		RawJSON:      bodyBytes,
	}

	return completion, nil
}

// buildPrompt concatenates messages into a single prompt string for the native API.
func buildPrompt(messages []api.Message) string {
	var parts []string
	for _, m := range messages {
		parts = append(parts, fmt.Sprintf("%s: %s", strings.ToLower(m.Role), m.Content))
	}
	return strings.Join(parts, "\n") + "\n"
}

// Stream sends a chat completion request and yields incremental chunks via SSE.
func (p *Provider) Stream(ctx context.Context, req api.CompletionRequest) (api.StreamReader, error) {
	if p.HTTPClient == nil {
		p.HTTPClient = &http.Client{Timeout: p.Timeout}
	}

	model := req.Model
	if model == "" {
		model = p.Model
	}

	body := ChatRequest{
		Prompt:      buildPrompt(req.Messages),
		NPredict:    req.MaxTokens,
		Model:       model,
		Temperature: req.Temperature,
		Stop:        req.StopSequences,
		Stream:      true,
		CachePrompt: p.CacheEnabled,
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request body: %w", err)
	}

	url := p.BaseURL + "/completion"

	reqHTTP, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(payload))
	if err != nil {
		return nil, wrapError(api.ErrProviderDown, fmt.Errorf("create request: %w", err))
	}

	reqHTTP.Header.Set("Content-Type", "application/json")
	reqHTTP.Header.Set("Accept", "text/event-stream")

	resp, err := p.HTTPClient.Do(reqHTTP)
	if err != nil {
		return nil, wrapError(api.ErrProviderDown, fmt.Errorf("HTTP request failed: %w", err))
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return nil, wrapError(api.ErrProviderDown, fmt.Errorf("provider returned HTTP %d: %s", resp.StatusCode, string(body)))
	}

	return newLlamaCPPStreamReader(resp, p.providerName), nil
}

// EmbedText returns a float32 vector for the given text.
// It auto-detects the embedding endpoint: /api/v1/embeddings (OpenAI-compatible) or
// /embeddings (llama.cpp-native). Falls back gracefully between them.
func (p *Provider) EmbedText(ctx context.Context, text string) ([]float32, error) {
	if p.HTTPClient == nil {
		p.HTTPClient = &http.Client{Timeout: p.Timeout}
	}

	body := EmbedRequest{
		Input:    text,
		Model:    normalizeQuant(p.Model),
		Truncate: true,
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal embedding request: %w", err)
	}

	// Try OpenAI-compatible endpoint first, then fall back to native /embeddings
	endpoints := []string{"/api/v1/embeddings", "/embeddings"}
	for _, ep := range endpoints {
		if p.EmbeddingEndpoint != "" {
			ep = p.EmbeddingEndpoint
		}

		fullURL := p.BaseURL + ep
		reqHTTP, err := http.NewRequestWithContext(ctx, "POST", fullURL, bytes.NewReader(payload))
		if err != nil {
			continue
		}
		reqHTTP.Header.Set("Content-Type", "application/json")

		resp, err := p.HTTPClient.Do(reqHTTP)
		if err != nil {
			continue
		}

		bodyBytes, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			continue
		}

		if resp.StatusCode != http.StatusOK {
			// If it's a 404 on the first endpoint, try the other
			if resp.StatusCode == http.StatusNotFound && ep == "/api/v1/embeddings" {
				continue
			}
			return nil, mapHTTPError(resp.StatusCode, string(bodyBytes))
		}

		// Try OpenAI-compatible format first (/api/v1/embeddings)
		var openAIResp EmbedResponse
		if err := json.Unmarshal(bodyBytes, &openAIResp); err == nil && len(openAIResp.Data) > 0 {
			return openAIResp.Data[0].Embedding, nil
		}

		// Try native /embeddings format
		var nativeResp NativeEmbedResponse
		if err := json.Unmarshal(bodyBytes, &nativeResp); err == nil && len(nativeResp.Embedding) > 0 {
			return nativeResp.Embedding, nil
		}

		// Neither format matched — return raw error
		return nil, wrapError(api.ErrProviderDown, fmt.Errorf("unexpected response shape from %s: %s", ep, string(bodyBytes)))
	}

	return nil, fmt.Errorf("no embedding endpoint available (tried /api/v1/embeddings and /embeddings)")
}

// ProviderInfo returns metadata about the underlying llama.cpp provider.
func (p *Provider) ProviderInfo() api.ProviderMetadata {
	return api.ProviderMetadata{
		Name:             p.providerName,
		SupportsTools:    true,
		SupportsStream:   true,
		MaxTokensPerCall: p.MaxTokens,
		BatchSupport:     false, // llama.cpp HTTP server doesn't expose batch endpoints at MVP
	}
}

// Logf logs with a [llm][{provider}] prefix.
func (p *Provider) Logf(format string, args ...any) {
	log.Printf("[llm][%s] "+format, append([]any{p.providerName}, args...)...)
}

// --- Helpers ---

func mapHTTPError(statusCode int, body string) error {
	switch statusCode {
	case http.StatusTooManyRequests:
		return api.ErrRateLimit
	case http.StatusUnprocessableEntity:
		if strings.Contains(strings.ToLower(body), "context window") ||
			strings.Contains(strings.ToLower(body), "max_tokens") {
			return api.ErrContextWindow
		}
		return wrapError(api.ErrProviderDown, fmt.Errorf("HTTP %d: %s", statusCode, body))

	case http.StatusInternalServerError:
		fallthrough
	case http.StatusBadGateway:
		fallthrough
	case http.StatusServiceUnavailable:
		return api.ErrProviderDown

	default:
		return fmt.Errorf("HTTP %d: %s", statusCode, body)
	}
}

func wrapError(typedErr *api.LLMError, original error) *api.LLMError {
	return &api.LLMError{
		Code:     typedErr.Code,
		Message:  typedErr.Message,
		Original: original,
	}
}

// --- Options ---

// Option configures a Provider.
type Option func(*Provider)

// WithCacheSize sets the maximum KV cache size in tokens.
func WithCacheSize(size int) Option {
	return func(p *Provider) { p.CacheSize = size }
}

// WithCacheType sets the KV cache precision type ("fq", "q4_0", "q8_0").
func WithCacheType(typ string) Option {
	return func(p *Provider) { p.CacheType = typ }
}

// WithCacheDisabled disables server-side prompt caching.
func WithCacheDisabled() Option {
	return func(p *Provider) { p.CacheEnabled = false }
}

// WithNativeEmbeddings configures the provider to use the native /embeddings endpoint
// instead of the OpenAI-compatible /api/v1/embeddings route. This is useful when
// you need --pooling none support or token-level embeddings.
func WithNativeEmbeddings() Option {
	return func(p *Provider) { p.EmbeddingEndpoint = "/embeddings" }
}

// WithOpenAICompatibleEmbeddings explicitly uses the /api/v1/embeddings endpoint.
func WithOpenAICompatibleEmbeddings() Option {
	return func(p *Provider) { p.EmbeddingEndpoint = "/api/v1/embeddings" }
}

// normalizeQuant ensures the quant suffix in a model identifier has the UD- prefix
// expected by llama.cpp's native API. For example, "foo:IQ3_S" becomes "foo:UD-IQ3_S".
func normalizeQuant(model string) string {
	if idx := strings.LastIndex(model, ":"); idx >= 0 {
		name := model[:idx]
		quant := model[idx+1:]
		if !strings.HasPrefix(quant, "UD-") {
			return name + ":UD-" + quant
		}
	}
	return model
}

// --- LlamaCPP SSE Stream Reader ---

// llamacppStreamReader yields incremental chunks from a llama.cpp native /completion SSE response.
// It handles the native llama.cpp /completion SSE format (with content, stop, stop_type, tokens_evaluated, tokens_cached, tokens_predicted, timings).
type llamacppStreamReader struct {
	resp             *http.Response
	done             chan struct{}
	providerName     string
	usage            *api.Usage // accumulated usage from SSE stream
	scanner          *bufio.Scanner
	contentBuilder   strings.Builder
	reasoningBuilder strings.Builder
	inReasoning      bool
	reasoningStarted bool
}

// newLlamaCPPStreamReader creates a new llama.cpp SSE stream reader.
func newLlamaCPPStreamReader(resp *http.Response, providerName string) api.StreamReader {
	return &llamacppStreamReader{
		resp:         resp,
		done:         make(chan struct{}),
		providerName: providerName,
		scanner:      bufio.NewScanner(resp.Body),
	}
}

func (s *llamacppStreamReader) Next() (api.Chunk, error) {
	for s.scanner.Scan() {
		line := s.scanner.Text()
		if line == "" {
			continue // empty line between events
		}

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		rawData := strings.TrimPrefix(line, "data: ")

		// Debug: expose raw SSE data for troubleshooting.
		log.Printf("[llm][%s] SSE raw data: %s", s.providerName, rawData)

		// Try parsing as JSON
		var delta map[string]interface{}
		if err := json.Unmarshal([]byte(rawData), &delta); err != nil {
			log.Printf("[llm][%s] SSE parse error: %v (raw: %q)", s.providerName, err, rawData)
			return api.Chunk{}, fmt.Errorf("SSE parse error: %w", err)
		}

		// Debug: show actual JSON keys to diagnose parser mismatch.
		keys := make([]string, 0, len(delta))
		for k := range delta {
			keys = append(keys, k)
		}
		log.Printf("[llm][%s] SSE delta keys: %v", s.providerName, keys)

		chunk := api.Chunk{}

		// Parse native llama.cpp format fields
		var nativeResp struct {
			Content         string `json:"content"`
			StopType        string `json:"stop_type,omitempty"`
			Stop            bool   `json:"stop,omitempty"`
			TokensEvaluated int    `json:"tokens_evaluated,omitempty"`
			TokensCached    int    `json:"tokens_cached,omitempty"`
			TokensPredicted int    `json:"tokens_predicted,omitempty"`
			Timings         *struct {
				PromptN          int     `json:"prompt_n,omitempty"`
				PredictN         int     `json:"predicted_n,omitempty"`
				PromptMS         float64 `json:"prompt_ms,omitempty"`
				PredictMS        float64 `json:"predicted_ms,omitempty"`
				PromptPerToken   float64 `json:"prompt_per_token_ms,omitempty"`
				PredictPerToken  float64 `json:"predicted_per_token_ms,omitempty"`
				PromptPerSecond  float64 `json:"prompt_per_second,omitempty"`
				PredictPerSecond float64 `json:"predicted_per_second,omitempty"`
				CacheN           int     `json:"cache_n,omitempty"`
			} `json:"timings,omitempty"`
		}
		if err := json.Unmarshal([]byte(rawData), &nativeResp); err != nil {
			log.Printf("[llm][%s] SSE native parse error: %v (raw: %q)", s.providerName, err, rawData)
			return api.Chunk{}, fmt.Errorf("SSE native parse error: %w", err)
		}

		// Determine stop reason
		finishReason := ""
		if nativeResp.StopType != "" {
			finishReason = nativeResp.StopType
		} else if nativeResp.Stop {
			finishReason = "stop"
		}

		// Process content and extract reasoning/thinking content
		if nativeResp.Content != "" {
			content := nativeResp.Content

			// Check for reasoning tags (llama.cpp reasoning format)
			// Handle < think> ... < /think> or <thinking> ... </thinking> tags
			hasReasoningStart := strings.Contains(content, "< think>") || strings.Contains(content, "<thinking>")

			if hasReasoningStart && !s.reasoningStarted {
				s.reasoningStarted = true
				s.inReasoning = true
			}

			// Extract reasoning content
			if strings.Contains(content, "< think>") {
				parts := strings.Split(content, "< think>")
				for i, part := range parts {
					if i == 0 {
						// Before first < think> tag - this is likely system prompt or initial content
						if !s.inReasoning {
							s.contentBuilder.WriteString(part)
							chunk.Delta = part
						}
						continue
					}
					if strings.Contains(part, "< /think>") {
						// End of reasoning content
						partsSplit := strings.Split(part, "< /think>")
						reasoningPart := partsSplit[0]
						s.reasoningBuilder.WriteString(reasoningPart)
						s.inReasoning = false

						// Remaining content is the final response
						remaining := ""
						if len(partsSplit) > 1 {
							remaining = partsSplit[1]
						}
						s.contentBuilder.WriteString(remaining)
						chunk.Delta = remaining
					} else {
						s.reasoningBuilder.WriteString(part)
						chunk.Delta = ""
					}
				}
			} else if strings.Contains(content, "<thinking>") {
				parts := strings.Split(content, "<thinking>")
				for i, part := range parts {
					if i == 0 {
						if !s.inReasoning {
							s.contentBuilder.WriteString(part)
							chunk.Delta = part
						}
						continue
					}
					if strings.Contains(part, "</thinking>") {
						reasoningPart := strings.Split(part, "</thinking>")[0]
						s.reasoningBuilder.WriteString(reasoningPart)
						s.inReasoning = false

						remaining := ""
						if len(strings.Split(part, "</thinking>")) > 1 {
							remaining = strings.Split(part, "</thinking>")[1]
						}
						s.contentBuilder.WriteString(remaining)
						chunk.Delta = remaining
					} else {
						s.reasoningBuilder.WriteString(part)
						chunk.Delta = ""
					}
				}
			} else {
				// No reasoning tags in this chunk
				if s.inReasoning {
					// We're in reasoning mode but no tags - accumulate to reasoning
					s.reasoningBuilder.WriteString(content)
					chunk.Delta = ""
				} else {
					s.contentBuilder.WriteString(content)
					chunk.Delta = content
				}
			}
		} else if nativeResp.Stop || nativeResp.StopType != "" {
			// Final chunk with token usage
			log.Printf("[llm][%s] SSE parsed (native final): stop_type=%q tokens_evaluated=%d tokens_cached=%d tokens_predicted=%d", s.providerName, nativeResp.StopType, nativeResp.TokensEvaluated, nativeResp.TokensCached, nativeResp.TokensPredicted)

			s.usage = &api.Usage{}
			promptTokens := nativeResp.TokensEvaluated
			if promptTokens == 0 && nativeResp.Timings != nil {
				promptTokens = nativeResp.Timings.PromptN
			}

			completionTokens := 0
			if nativeResp.TokensPredicted > 0 {
				completionTokens = nativeResp.TokensPredicted
			} else if nativeResp.TokensEvaluated > 0 && nativeResp.TokensCached > 0 {
				completionTokens = nativeResp.TokensEvaluated - nativeResp.TokensCached
			} else if nativeResp.Timings != nil && nativeResp.Timings.PredictN > 0 {
				completionTokens = nativeResp.Timings.PredictN
			}

			totalTokens := promptTokens + completionTokens
			if totalTokens > 0 {
				s.usage.PromptTokens = promptTokens
				s.usage.CompletionTokens = completionTokens
				s.usage.TotalTokens = totalTokens
				s.usage.CachedTokens = nativeResp.TokensCached

				// Populate timing fields
				if nativeResp.Timings != nil {
					s.usage.CacheN = nativeResp.Timings.CacheN
					s.usage.PromptN = nativeResp.Timings.PromptN
					s.usage.PredictN = nativeResp.Timings.PredictN
					s.usage.PromptMS = nativeResp.Timings.PromptMS
					s.usage.PredictMS = nativeResp.Timings.PredictMS
					s.usage.PromptPerTokenMS = nativeResp.Timings.PromptPerToken
					s.usage.PredictPerTokenMS = nativeResp.Timings.PredictPerToken
					s.usage.PromptPerSecond = nativeResp.Timings.PromptPerSecond
					s.usage.PredictPerSecond = nativeResp.Timings.PredictPerSecond
				}

				log.Printf("[llm][%s] SSE parsed (native final) usage: prompt=%d completion=%d total=%d cached=%d", s.providerName, promptTokens, completionTokens, totalTokens, nativeResp.TokensCached)
			}
		}

		chunk.FinishReason = finishReason
		if s.usage != nil {
			chunk.Usage = s.usage
		}

		// Set reasoning content if we have it
		if s.reasoningBuilder.Len() > 0 {
			chunk.ReasoningContent = s.reasoningBuilder.String()
		}

		log.Printf("[llm][%s] SSE parsed (llamacpp): delta=%q finish_reason=%q reasoning_content_len=%d", s.providerName, chunk.Delta, chunk.FinishReason, len(chunk.ReasoningContent))

		return chunk, nil
	}
	if err := s.scanner.Err(); err != nil {
		return api.Chunk{}, fmt.Errorf("scanner error: %w", err)
	}
	return api.Chunk{FinishReason: "[done]"}, nil
}

func (s *llamacppStreamReader) Close() error {
	if s.resp != nil && s.resp.Body != nil {
		return s.resp.Body.Close()
	}
	if s.done != nil {
		close(s.done)
	}
	return nil
}
