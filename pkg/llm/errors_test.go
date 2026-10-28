package llm

import (
	"errors"
	"fmt"
	"testing"
)

func TestLLMError_Error(t *testing.T) {
	err := &LLMError{Code: "rate_limit", Message: "API rate limit exceeded"}
	got := err.Error()
	if got != "[rate_limit] API rate limit exceeded" {
		t.Errorf("Error() = %q; want '[rate_limit] API rate limit exceeded'", got)
	}
}

func TestLLMError_ErrorWithOriginal(t *testing.T) {
	original := fmt.Errorf("underlying cause")
	err := &LLMError{Code: "context_window", Message: "Input exceeds context window", Original: original}
	got := err.Error()
	want := "[context_window] Input exceeds context window: underlying cause"
	if got != want {
		t.Errorf("Error() = %q; want %q", got, want)
	}
}

func TestLLMError_Unwrap(t *testing.T) {
	original := errors.New("network timeout")
	err := &LLMError{Code: "provider_down", Message: "Provider endpoint unavailable", Original: original}

	unwrapped := err.Unwrap()
	if unwrapped == nil {
		t.Fatal("Unwrap() returned nil; expected wrapped error")
	}
	if unwrapped.Error() != "network timeout" {
		t.Errorf("Unwrap() = %v; want 'network timeout'", unwrapped)
	}
}

func TestIsRateLimit(t *testing.T) {
	rateErr := &LLMError{Code: "rate_limit", Message: "too many requests"}
	if !IsRateLimit(rateErr) {
		t.Error("expected IsRateLimit to return true for rate_limit error")
	}

	wrapped := fmt.Errorf("wrapped: %w", rateErr)
	if !IsRateLimit(wrapped) {
		t.Error("expected IsRateLimit to return true for wrapped rate_limit error")
	}

	otherErr := &LLMError{Code: "context_window", Message: "too long"}
	if IsRateLimit(otherErr) {
		t.Error("expected IsRateLimit to return false for context_window error")
	}

	if IsRateLimit(nil) {
		t.Error("expected IsRateLimit to return false for nil")
	}

	if IsRateLimit(errors.New("plain error")) {
		t.Error("expected IsRateLimit to return false for plain error")
	}
}

func TestIsContextWindow(t *testing.T) {
	cwErr := &LLMError{Code: "context_window", Message: "input too long"}
	if !IsContextWindow(cwErr) {
		t.Error("expected IsContextWindow to return true")
	}

	rateErr := &LLMError{Code: "rate_limit", Message: "too many requests"}
	if IsContextWindow(rateErr) {
		t.Error("expected false for rate_limit error")
	}
}

func TestIsProviderDown(t *testing.T) {
	downErr := &LLMError{Code: "provider_down", Message: "unreachable"}
	if !IsProviderDown(downErr) {
		t.Error("expected IsProviderDown to return true")
	}

	cwErr := &LLMError{Code: "context_window", Message: "too long"}
	if IsProviderDown(cwErr) {
		t.Error("expected false for context_window error")
	}
}

func TestGlobalErrors(t *testing.T) {
	if ErrRateLimit == nil {
		t.Error("ErrRateLimit should not be nil")
	}
	if !IsRateLimit(ErrRateLimit) {
		t.Error("IsRateLimit should return true for ErrRateLimit")
	}

	if ErrContextWindow == nil {
		t.Error("ErrContextWindow should not be nil")
	}
	if !IsContextWindow(ErrContextWindow) {
		t.Error("IsContextWindow should return true for ErrContextWindow")
	}

	if ErrProviderDown == nil {
		t.Error("ErrProviderDown should not be nil")
	}
	if !IsProviderDown(ErrProviderDown) {
		t.Error("IsProviderDown should return true for ErrProviderDown")
	}
}
