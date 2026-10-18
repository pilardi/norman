// Package llm provides typed error types for provider-specific failures.
package llm

import "fmt"

// ErrRateLimit is returned when the API reports a rate-limit or quota-exceeded response.
var ErrRateLimit = &LLMError{Code: "rate_limit", Message: "API rate limit exceeded"}

// ErrContextWindow is returned when the input exceeds the provider's context window.
var ErrContextWindow = &LLMError{Code: "context_window", Message: "Input exceeds context window"}

// ErrProviderDown is returned when the provider endpoint is unreachable or returns a server error.
var ErrProviderDown = &LLMError{Code: "provider_down", Message: "Provider endpoint unavailable"}

// LLMError is a typed error wrapping provider-specific failure details.
type LLMError struct {
	Code    string // machine-readable error code (e.g., "rate_limit", "context_window")
	Message string // human-readable description
	Original error  // the wrapped underlying error, if any
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

// IsRateLimit checks whether the given error is a rate-limit error.
func IsRateLimit(err error) bool {
	if lerr, ok := err.(*LLMError); ok && lerr.Code == "rate_limit" {
		return true
	}
	return false
}

// IsContextWindow checks whether the given error is a context-window error.
func IsContextWindow(err error) bool {
	if lerr, ok := err.(*LLMError); ok && lerr.Code == "context_window" {
		return true
	}
	return false
}

// IsProviderDown checks whether the given error indicates the provider is down.
func IsProviderDown(err error) bool {
	if lerr, ok := err.(*LLMError); ok && lerr.Code == "provider_down" {
		return true
	}
	return false
}
