// Package sse provides a unified SSE stream reader for LLM providers.
package sse

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/norman/norman/pkg/llm/api"
)

// sseStreamReader yields incremental chunks from a streaming SSE response.
// It handles both OpenAI-compatible SSE format (with choices, usage, timings, reasoning_content, tool_calls)
// and native llama.cpp /completion SSE format (with content, stop_reason, tokens_evaluated, tokens_cached, timings).
type sseStreamReader struct {
	resp         *http.Response
	done         chan struct{}
	providerName string
	usage        *api.Usage // accumulated usage from SSE stream
}

// NewStreamReader creates a new SSE stream reader.
func NewStreamReader(resp *http.Response, providerName string) api.StreamReader {
	return &sseStreamReader{
		resp:         resp,
		done:         make(chan struct{}),
		providerName: providerName,
	}
}

func (s *sseStreamReader) Next() (api.Chunk, error) {
	for {
		line, err := readSSELine(s.resp.Body)
		if err != nil {
			// Handle EOF gracefully
			if err == io.EOF {
				return api.Chunk{FinishReason: "[done]"}, nil
			}
			return api.Chunk{}, fmt.Errorf("read SSE: %w", err)
		}
		if line == "" {
			continue // empty line between events
		}

		if line == "data: [DONE]" {
			chunk := api.Chunk{FinishReason: "[done]"}
			if s.usage != nil {
				chunk.Usage = s.usage
			}
			return chunk, nil
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
			// Try parsing as native llama.cpp format
			var nativeResp struct {
				Content         string `json:"content"`
				StopReason      string `json:"stop_reason,omitempty"`
				TokensEvaluated int    `json:"tokens_evaluated,omitempty"`
				TokensCached    int    `json:"tokens_cached,omitempty"`
				Timings         *struct {
					PromptN          int     `json:"prompt_n,omitempty"`
					PredictN         int     `json:"predict_n,omitempty"`
					PromptMS         float64 `json:"prompt_ms,omitempty"`
					PredictMS        float64 `json:"predict_ms,omitempty"`
					PromptPerToken   float64 `json:"prompt_per_token_ms,omitempty"`
					PredictPerToken  float64 `json:"predicted_per_token_ms,omitempty"`
					PromptPerSecond  float64 `json:"prompt_per_second,omitempty"`
					PredictPerSecond float64 `json:"predicted_per_second,omitempty"`
					CacheN           int     `json:"cache_n,omitempty"`
				} `json:"timings,omitempty"`
			}
			if err2 := json.Unmarshal([]byte(rawData), &nativeResp); err2 == nil {
				chunk := api.Chunk{FinishReason: nativeResp.StopReason}
				if nativeResp.Content != "" {
					chunk.Delta = nativeResp.Content
					log.Printf("[llm][%s] SSE parsed (native): content=%q stop_reason=%q", s.providerName, nativeResp.Content, nativeResp.StopReason)
				} else if nativeResp.StopReason != "" {
					// Final chunk with token usage
					log.Printf("[llm][%s] SSE parsed (native final): stop_reason=%q tokens_evaluated=%d tokens_cached=%d", s.providerName, nativeResp.StopReason, nativeResp.TokensEvaluated, nativeResp.TokensCached)

					s.usage = &api.Usage{}
					promptTokens := nativeResp.TokensEvaluated
					if promptTokens == 0 && nativeResp.Timings != nil {
						promptTokens = nativeResp.Timings.PromptN
					}

					completionTokens := 0
					if nativeResp.TokensEvaluated > 0 && nativeResp.TokensCached > 0 {
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
				if s.usage != nil {
					chunk.Usage = s.usage
				}
				return chunk, nil
			}

			// Do not silently skip — log the full raw data for debugging.
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

		// Check if it's the final chunk with choices: [] and usage/timings
		choices, isChoices := delta["choices"].([]interface{})
		if isChoices && len(choices) == 0 {
			// Final chunk: {"choices":[],"created":...,"usage":...,"timings":...}
			if usageMap, ok := delta["usage"].(map[string]interface{}); ok {
				s.usage = &api.Usage{}
				if promptTokens, ok := usageMap["prompt_tokens"].(float64); ok {
					s.usage.PromptTokens = int(promptTokens)
				}
				if completionTokens, ok := usageMap["completion_tokens"].(float64); ok {
					s.usage.CompletionTokens = int(completionTokens)
				}
				if totalTokens, ok := usageMap["total_tokens"].(float64); ok {
					s.usage.TotalTokens = int(totalTokens)
				}
				// Extract cached_tokens from usage or prompt_tokens_details
				if cachedTokens, ok := usageMap["cached_tokens"].(float64); ok {
					s.usage.CachedTokens = int(cachedTokens)
				} else if promptTokensDetails, ok := usageMap["prompt_tokens_details"].(map[string]interface{}); ok {
					if cachedTokens, ok := promptTokensDetails["cached_tokens"].(float64); ok {
						s.usage.CachedTokens = int(cachedTokens)
					}
				}
			}
			// Extract timings
			log.Printf("[llm][%s] Checking timings map: exists=%v", s.providerName, delta["timings"] != nil)
			if timingsMap, ok := delta["timings"].(map[string]interface{}); ok {
				log.Printf("[llm][%s] Timings map extracted successfully, keys: %v", s.providerName, getMapKeys(timingsMap))
				if s.usage == nil {
					s.usage = &api.Usage{}
				}
				if cacheN, ok := extractFloat(timingsMap, "cache_n"); ok {
					s.usage.CacheN = int(cacheN)
				}
				if promptN, ok := extractFloat(timingsMap, "prompt_n"); ok {
					s.usage.PromptN = int(promptN)
				}
				if predictN, ok := extractFloat(timingsMap, "predicted_n"); ok {
					s.usage.PredictN = int(predictN)
				}
				if promptMS, ok := extractFloat(timingsMap, "prompt_ms"); ok {
					s.usage.PromptMS = promptMS
				}
				if predictMS, ok := extractFloat(timingsMap, "predicted_ms"); ok {
					s.usage.PredictMS = predictMS
				}
				if promptPerTokenMS, ok := extractFloat(timingsMap, "prompt_per_token_ms"); ok {
					s.usage.PromptPerTokenMS = promptPerTokenMS
				}
				if predictPerTokenMS, ok := extractFloat(timingsMap, "predicted_per_token_ms"); ok {
					s.usage.PredictPerTokenMS = predictPerTokenMS
				}
				if promptPerSecond, ok := extractFloat(timingsMap, "prompt_per_second"); ok {
					s.usage.PromptPerSecond = promptPerSecond
				}
				if predictPerSecond, ok := extractFloat(timingsMap, "predicted_per_second"); ok {
					s.usage.PredictPerSecond = predictPerSecond
				}
				log.Printf("[llm][%s] Timings extracted: CacheN=%d PromptN=%d PredictN=%d PromptMS=%f PredictMS=%f", s.providerName, s.usage.CacheN, s.usage.PromptN, s.usage.PredictN, s.usage.PromptMS, s.usage.PredictMS)
			} else {
				log.Printf("[llm][%s] Failed to extract timings map from delta", s.providerName)
			}
			chunk.FinishReason = "[done]"
			if s.usage != nil {
				chunk.Usage = s.usage
			}
			return chunk, nil
		}

		// OpenAI SSE format: {"choices":[{"delta":{"content":"...","tool_calls":[...]},"finish_reason":null}]}
		if choices, ok := delta["choices"].([]interface{}); ok && len(choices) > 0 {
			if choice, ok := choices[0].(map[string]interface{}); ok {
				if deltaMap, ok := choice["delta"].(map[string]interface{}); ok {
					if content, ok := deltaMap["content"].(string); ok {
						chunk.Delta = content
					}
					// Extract reasoning/thinking content (for models like o1/o3 that support it).
					if reasoning, ok := deltaMap["reasoning_content"].(string); ok {
						chunk.ReasoningContent = reasoning
					}
					// Extract tool calls from delta.tool_calls array.
					if tcArr, ok := deltaMap["tool_calls"].([]interface{}); ok && len(tcArr) > 0 {
						for _, tcRaw := range tcArr {
							tcMap, ok := tcRaw.(map[string]interface{})
							if !ok {
								continue
							}
							id, _ := tcMap["id"].(string)
							fnRaw, _ := tcMap["function"].(map[string]interface{})
							name, _ := fnRaw["name"].(string)
							// Arguments are normally a JSON string; handle map if provider returns parsed object.
							argsRaw, ok := fnRaw["arguments"].(string)
							if !ok {
								if m, ok := fnRaw["arguments"].(map[string]interface{}); ok {
									b, _ := json.Marshal(m)
									argsRaw = string(b)
								}
							}
							chunk.ToolCalls = append(chunk.ToolCalls, api.ToolCall{
								ID:        id,
								Name:      name,
								Arguments: json.RawMessage(argsRaw),
							})
						}
					}
				}
				if fr, ok := choice["finish_reason"].(string); ok {
					chunk.FinishReason = fr
				}
			}
		}

		// Extract usage from SSE event if present at root level
		if usageMap, ok := delta["usage"].(map[string]interface{}); ok {
			s.usage = &api.Usage{}
			if promptTokens, ok := usageMap["prompt_tokens"].(float64); ok {
				s.usage.PromptTokens = int(promptTokens)
			}
			if completionTokens, ok := usageMap["completion_tokens"].(float64); ok {
				s.usage.CompletionTokens = int(completionTokens)
			}
			if totalTokens, ok := usageMap["total_tokens"].(float64); ok {
				s.usage.TotalTokens = int(totalTokens)
			}
			// Extract cached_tokens from usage or prompt_tokens_details
			if cachedTokens, ok := usageMap["cached_tokens"].(float64); ok {
				s.usage.CachedTokens = int(cachedTokens)
			} else if promptTokensDetails, ok := usageMap["prompt_tokens_details"].(map[string]interface{}); ok {
				if cachedTokens, ok := promptTokensDetails["cached_tokens"].(float64); ok {
					s.usage.CachedTokens = int(cachedTokens)
				}
			}
			// Extract timings if present at root level
			if timingsMap, ok := delta["timings"].(map[string]interface{}); ok {
				if s.usage == nil {
					s.usage = &api.Usage{}
				}
				if cacheN, ok := extractFloat(timingsMap, "cache_n"); ok {
					s.usage.CacheN = int(cacheN)
				}
				if promptN, ok := extractFloat(timingsMap, "prompt_n"); ok {
					s.usage.PromptN = int(promptN)
				}
				if predictN, ok := extractFloat(timingsMap, "predicted_n"); ok {
					s.usage.PredictN = int(predictN)
				}
				if promptMS, ok := extractFloat(timingsMap, "prompt_ms"); ok {
					s.usage.PromptMS = promptMS
				}
				if predictMS, ok := extractFloat(timingsMap, "predicted_ms"); ok {
					s.usage.PredictMS = predictMS
				}
				if promptPerTokenMS, ok := extractFloat(timingsMap, "prompt_per_token_ms"); ok {
					s.usage.PromptPerTokenMS = promptPerTokenMS
				}
				if predictPerTokenMS, ok := extractFloat(timingsMap, "predicted_per_token_ms"); ok {
					s.usage.PredictPerTokenMS = predictPerTokenMS
				}
				if promptPerSecond, ok := extractFloat(timingsMap, "prompt_per_second"); ok {
					s.usage.PromptPerSecond = promptPerSecond
				}
				if predictPerSecond, ok := extractFloat(timingsMap, "predicted_per_second"); ok {
					s.usage.PredictPerSecond = predictPerSecond
				}
			}
		}

		log.Printf("[llm][%s] SSE parsed: delta=%q finish_reason=%q tool_calls=%d", s.providerName, chunk.Delta, chunk.FinishReason, len(chunk.ToolCalls))

		return chunk, nil
	}
}

func (s *sseStreamReader) Close() error {
	_ = s.resp.Body.Close()
	if s.done != nil {
		close(s.done)
	}
	return nil
}

// readSSELine reads a single SSE event line from the response body.
func readSSELine(r io.Reader) (string, error) {
	buf := make([]byte, 4096)
	n, err := r.Read(buf)
	if err != nil {
		return "", err
	}
	data := buf[:n]
	// Split on newlines and return the last non-empty segment.
	// This handles cases where [DONE] is concatenated with the preceding data chunk.
	parts := bytes.Split(data, []byte("\n"))
	for i := len(parts) - 1; i >= 0; i-- {
		line := string(bytes.TrimSpace(parts[i]))
		if line != "" {
			return line, nil
		}
	}
	return "", nil
}

// getMapKeys returns the keys of a map[string]interface{}.
func getMapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// extractFloat extracts a float64 value from a map[string]interface{} by key.
func extractFloat(m map[string]interface{}, key string) (float64, bool) {
	val, ok := m[key]
	if !ok {
		return 0, false
	}
	switch v := val.(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case json.Number:
		f, err := v.Float64()
		if err != nil {
			return 0, false
		}
		return f, true
	default:
		return 0, false
	}
}
