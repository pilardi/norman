// Package llamacpp provides a provider implementation for llama.cpp's HTTP server.
// It implements the LLM and Embedder interfaces with explicit support for
// KV-cache prompt caching optimization.
package llamacpp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/norman/norman/pkg/config"
	"github.com/norman/norman/pkg/llm"
)

// ProviderName identifies this provider.
const ProviderName = "llamacpp"

// --- Request / Response Types ---

// ChatRequest mirrors the llama.cpp native /completion endpoint body.
type ChatRequest struct {
	Prompt     string `json:"prompt"`
	NPredict   int    `json:"n_predict,omitempty"`
	Model      string `json:"model"`
	Stream     bool   `json:"stream"`
	Temperature float64 `json:"temperature,omitempty"`
	Stop       []string `json:"stop,omitempty"`
	CachePrompt bool   `json:"cache_prompt,omitempty"`
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
	Data   []EmbedData `json:"data"`
	Usage  Usage       `json:"usage"`
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

// Provider implements llm.LLM + llm.Embedder for llama.cpp's HTTP server.
type Provider struct {
	BaseURL          string
	Model            string
	Temperature        float64
	MaxTokens        int
	HTTPClient       *http.Client
	Timeout          time.Duration
	CacheEnabled     bool   // whether --cache-prompt is active on the server
	CacheSize        int    // max KV cache tokens (n_ctx)
	CacheType        string // "fq" or quantized type
	EmbeddingEndpoint string // embedding endpoint path; "" = auto-detect, "/api/v1/embeddings", "/embeddings"
}

// NewProvider creates a provider configured from workspace config and
// optional llama.cpp-specific settings.
func NewProvider(cfg *config.WorkspaceConfig, llmCfg *Config, opts ...Option) *Provider {
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
	}

	for _, opt := range opts {
		opt(p)
	}

	return p
}

// Complete sends a chat completion request and returns the full response.
func (p *Provider) Complete(ctx context.Context, req llm.CompletionRequest) (*llm.CompletionResponse, error) {
	if p.HTTPClient == nil {
		p.HTTPClient = &http.Client{Timeout: p.Timeout}
	}

	body := ChatRequest{
		Prompt:      buildPrompt(req.Messages),
		NPredict:    req.MaxTokens,
		Model:       req.Model,
		Temperature:   req.Temperature,
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
		return nil, wrapError(llm.ErrProviderDown, fmt.Errorf("create request: %w", err))
	}

	reqHTTP.Header.Set("Content-Type", "application/json")

	resp, err := p.HTTPClient.Do(reqHTTP)
	if err != nil {
		return nil, wrapError(llm.ErrProviderDown, fmt.Errorf("HTTP request failed: %w", err))
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, wrapError(llm.ErrProviderDown, fmt.Errorf("read response body: %w", err))
	}

	if resp.StatusCode != http.StatusOK {
		return nil, mapHTTPError(resp.StatusCode, string(bodyBytes))
	}

	var chatResp ChatResponse
	if err := json.Unmarshal(bodyBytes, &chatResp); err != nil {
		return nil, wrapError(llm.ErrProviderDown, fmt.Errorf("parse response: %w", err))
	}

	completion := &llm.CompletionResponse{
		ID:           "", // native API doesn't return an ID
		Model:        req.Model,
		Message:      llm.Message{Role: "assistant", Content: chatResp.Content},
		FinishReason: chatResp.StopReason,
		RawJSON:      bodyBytes,
	}

	return completion, nil
}

// buildPrompt concatenates messages into a single prompt string for the native API.
func buildPrompt(messages []llm.Message) string {
	var parts []string
	for _, m := range messages {
		parts = append(parts, fmt.Sprintf("%s: %s", strings.ToLower(m.Role), m.Content))
	}
	return strings.Join(parts, "\n") + "\n"
}

// Stream sends a chat completion request and yields incremental chunks via SSE.
func (p *Provider) Stream(ctx context.Context, req llm.CompletionRequest) (llm.StreamReader, error) {
	if p.HTTPClient == nil {
		p.HTTPClient = &http.Client{Timeout: p.Timeout}
	}

	body := ChatRequest{
		Prompt:      buildPrompt(req.Messages),
		NPredict:    req.MaxTokens,
		Model:       req.Model,
		Temperature:   req.Temperature,
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
		return nil, wrapError(llm.ErrProviderDown, fmt.Errorf("create request: %w", err))
	}

	reqHTTP.Header.Set("Content-Type", "application/json")
	reqHTTP.Header.Set("Accept", "text/event-stream")

	resp, err := p.HTTPClient.Do(reqHTTP)
	if err != nil {
		return nil, wrapError(llm.ErrProviderDown, fmt.Errorf("HTTP request failed: %w", err))
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, wrapError(llm.ErrProviderDown, fmt.Errorf("provider returned HTTP %d: %s", resp.StatusCode, string(body)))
	}

	return &sseStreamReader{resp: resp}, nil
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
		Model:    p.Model,
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
		resp.Body.Close()
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
		return nil, wrapError(llm.ErrProviderDown, fmt.Errorf("unexpected response shape from %s: %s", ep, string(bodyBytes)))
	}

	return nil, fmt.Errorf("no embedding endpoint available (tried /api/v1/embeddings and /embeddings)")
}

// ProviderInfo returns metadata about the underlying llama.cpp provider.
func (p *Provider) ProviderInfo() llm.ProviderMetadata {
	return llm.ProviderMetadata{
		Name:             ProviderName,
		SupportsTools:    true,
		SupportsStream:   true,
		MaxTokensPerCall: p.MaxTokens,
		BatchSupport:     false, // llama.cpp HTTP server doesn't expose batch endpoints at MVP
	}
}

// --- SSE Streaming Reader ---

type sseStreamReader struct {
	resp *http.Response
}

func (s *sseStreamReader) Next() (llm.Chunk, error) {
	buf := make([]byte, 4096)
	n, err := s.resp.Body.Read(buf)
	if err != nil {
		return llm.Chunk{}, fmt.Errorf("read SSE: %w", err)
	}

	line := strings.TrimSpace(string(buf[:n]))
	if line == "" || line == "data: [DONE]" {
		return llm.Chunk{FinishReason: "[done]"}, nil
	}

	if !strings.HasPrefix(line, "data: ") {
		return s.Next() // skip non-data lines
	}

	data := strings.TrimPrefix(line, "data: ")

	// Native /completion format: {"content":"...","stop_reason":"..."}
	var nativeResp struct {
		Content    string `json:"content"`
		StopReason string `json:"stop_reason,omitempty"`
	}
	if err := json.Unmarshal([]byte(data), &nativeResp); err == nil && nativeResp.Content != "" {
		return llm.Chunk{Delta: nativeResp.Content, FinishReason: nativeResp.StopReason}, nil
	}

	// Fallback to OpenAI-compatible format for backward compat
	var delta map[string]interface{}
	if err := json.Unmarshal([]byte(data), &delta); err == nil {
		chunk := llm.Chunk{}
		if msg, ok := delta["message"].(map[string]interface{}); ok {
			if content, ok := msg["content"].(string); ok {
				chunk.Delta = content
			}
		}
		if fr, ok := delta["finish_reason"].(string); ok {
			chunk.FinishReason = fr
		}
		return chunk, nil
	}

	return llm.Chunk{}, fmt.Errorf("unmarshal SSE data: %w", err)
}

func (s *sseStreamReader) Close() error {
	s.resp.Body.Close()
	return nil
}

// --- Helpers ---

func mapHTTPError(statusCode int, body string) error {
	switch statusCode {
	case http.StatusTooManyRequests:
		return llm.ErrRateLimit
	case http.StatusUnprocessableEntity:
		if strings.Contains(strings.ToLower(body), "context window") ||
			strings.Contains(strings.ToLower(body), "max_tokens") {
			return llm.ErrContextWindow
		}
		return wrapError(llm.ErrProviderDown, fmt.Errorf("HTTP %d: %s", statusCode, body))

	case http.StatusInternalServerError:
		fallthrough
	case http.StatusBadGateway:
		fallthrough
	case http.StatusServiceUnavailable:
		return llm.ErrProviderDown

	default:
		return fmt.Errorf("HTTP %d: %s", statusCode, body)
	}
}

func wrapError(typedErr *llm.LLMError, original error) *llm.LLMError {
	return &llm.LLMError{
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
