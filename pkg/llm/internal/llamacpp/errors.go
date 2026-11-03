// Package llamacpp provides typed error types for llama.cpp provider failures.
package llamacpp

import "fmt"

// ErrKVCacheFull is returned when the KV cache is exhausted and cannot store new tokens.
var ErrKVCacheFull = &LLMError{Code: "kv_cache_full", Message: "KV cache capacity exceeded"}

// ErrPromptTooLong is returned when a single prompt exceeds the configured cache size.
var ErrPromptTooLong = &LLMError{Code: "prompt_too_long", Message: "Prompt exceeds KV cache size"}

// ErrCacheCorrupt is returned when a cached KV state cannot be restored (data corruption).
var ErrCacheCorrupt = &LLMError{Code: "cache_corrupt", Message: "KV cache data corrupted or inconsistent"}

// LLMError is a typed error wrapping provider-specific failure details.
type LLMError struct {
	Code     string // machine-readable code (e.g., "kv_cache_full")
	Message  string // human-readable description
	Original error  // the wrapped underlying error
}

func (e *LLMError) Error() string {
	if e.Original != nil {
		return fmt.Sprintf("[%s] %s: %v", e.Code, e.Message, e.Original)
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

func (e *LLMError) Unwrap() error {
	return e.Original
}

// IsKVCacheFull checks whether the given error is a KV-cache-full error.
func IsKVCacheFull(err error) bool {
	if lerr, ok := err.(*LLMError); ok && lerr.Code == "kv_cache_full" {
		return true
	}
	return false
}

// IsPromptTooLong checks whether the given error is a prompt-too-long error.
func IsPromptTooLong(err error) bool {
	if lerr, ok := err.(*LLMError); ok && lerr.Code == "prompt_too_long" {
		return true
	}
	return false
}

// IsCacheCorrupt checks whether the given error indicates cache corruption.
func IsCacheCorrupt(err error) bool {
	if lerr, ok := err.(*LLMError); ok && lerr.Code == "cache_corrupt" {
		return true
	}
	return false
}
