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

// ChatRequest mirrors the llama.cpp HTTP server's chat endpoint body.
type ChatRequest struct {
	Model        string   `json:"model"`
	Messages     []llm.Message `json:"messages"`
	Tools        []llm.Tool     `json:"tools,omitempty"`
	Stream       bool     `json:"stream"`
	Temperature    float64  `json:"temperature"`
	MaxTokens    int      `json:"max_tokens"`
	Stop         []string `json:"stop,omitempty"`
	PresencePenalty  float32 `json:"presence_penalty,omitempty"`
	FrequencyPenalty float32 `json:"frequency_penalty,omitempty"`
	CachePrompt  bool     `json:"cache_prompt,omitempty"` // opt-in per-request cache usage
}

// ChatResponse is the llama.cpp chat completion response.
type ChatResponse struct {
	ID           string    `json:"id"`
	Model        string    `json:"model"`
	Choices      []Choice  `json:"choices"`
	Usage        Usage     `json:"usage"`
	RawJSON      []byte    `json:"-"`
}

// Choice mirrors a single choice in the response.
type Choice struct {
	Message      llm.Message `json:"message"`
	FinishReason string      `json:"finish_reason"`
	Index        int         `json:"index"`
}

// Usage matches llama.cpp's usage reporting (includes cached_tokens).
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CachedTokens     int `json:"cached_tokens,omitempty"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// EmbedRequest is the body for /api/v1/embeddings.
type EmbedRequest struct {
	Input  string `json:"input"`
	Model  string `json:"model,omitempty"`
	Truncate bool `json:"truncate,omitempty"`
}

// EmbedResponse is the embedding result.
type EmbedResponse struct {
	Data   []EmbedData `json:"data"`
	Usage  Usage       `json:"usage"`
}

// EmbedData holds a single embedding vector.
type EmbedData struct {
	Object string    `json:"object"`
	Index  int       `json:"index"`
	Embedding []float32 `json:"embedding"`
}

// --- Provider Implementation ---

// Provider implements llm.LLM + llm.Embedder for llama.cpp's HTTP server.
type Provider struct {
	BaseURL      string
	Model        string
	Temperature  float64
	MaxTokens    int
	HTTPClient   *http.Client
	Timeout      time.Duration
	CacheEnabled bool   // whether --cache-prompt is active on the server
	CacheSize    int    // max KV cache tokens (n_ctx)
	CacheType    string // "fq" or quantized type
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
		Model:        req.Model,
		Messages:     req.Messages,
		Tools:        req.Tools,
		Temperature:    req.Temperature,
		MaxTokens:    req.MaxTokens,
		Stop:         req.StopSequences,
		PresencePenalty:  req.PresencePenalty,
		FrequencyPenalty: req.FrequencyPenalty,
		CachePrompt:  p.CacheEnabled, // opt-in to server-side KV cache
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request body: %w", err)
	}

	url := p.BaseURL + "/api/v1/chat"

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

	// Convert to universal CompletionResponse
	completion := &llm.CompletionResponse{
		ID:           chatResp.ID,
		Model:        chatResp.Model,
		Message:      chatResp.Choices[0].Message,
		FinishReason: chatResp.Choices[0].FinishReason,
		Usage: llm.Usage{
			PromptTokens:     chatResp.Usage.PromptTokens,
			CompletionTokens: chatResp.Usage.CompletionTokens,
			TotalTokens:      chatResp.Usage.TotalTokens,
		},
		RawJSON: bodyBytes,
	}

	return completion, nil
}

// Stream sends a chat completion request and yields incremental chunks via SSE.
func (p *Provider) Stream(ctx context.Context, req llm.CompletionRequest) (llm.StreamReader, error) {
	if p.HTTPClient == nil {
		p.HTTPClient = &http.Client{Timeout: p.Timeout}
	}

	body := ChatRequest{
		Model:        req.Model,
		Messages:     req.Messages,
		Tools:        req.Tools,
		Temperature:    req.Temperature,
		MaxTokens:    req.MaxTokens,
		Stop:         req.StopSequences,
		PresencePenalty:  req.PresencePenalty,
		FrequencyPenalty: req.FrequencyPenalty,
		Stream:       true,
		CachePrompt:  p.CacheEnabled,
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request body: %w", err)
	}

	url := p.BaseURL + "/api/v1/chat"

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

	return &sseStreamReader{resp: resp}, nil
}

// EmbedText returns a float32 vector for the given text.
func (p *Provider) EmbedText(ctx context.Context, text string) ([]float32, error) {
	if p.HTTPClient == nil {
		p.HTTPClient = &http.Client{Timeout: p.Timeout}
	}

	body := EmbedRequest{
		Input:  text,
		Model:  p.Model,
		Truncate: true,
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal embedding request: %w", err)
	}

	url := p.BaseURL + "/api/v1/embeddings"

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

	var embedResp EmbedResponse
	if err := json.Unmarshal(bodyBytes, &embedResp); err != nil {
		return nil, wrapError(llm.ErrProviderDown, fmt.Errorf("parse response: %w", err))
	}

	if len(embedResp.Data) == 0 {
		return nil, fmt.Errorf("no embedding data returned")
	}

	return embedResp.Data[0].Embedding, nil
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
	var delta map[string]interface{}
	if err := json.Unmarshal([]byte(data), &delta); err != nil {
		return llm.Chunk{}, fmt.Errorf("unmarshal SSE data: %w", err)
	}

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
