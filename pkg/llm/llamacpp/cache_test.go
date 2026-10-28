package llamacpp

import (
	"testing"
)

func TestDefaultStrategy(t *testing.T) {
	s := DefaultStrategy()

	if !s.Enabled {
		t.Error("expected Enabled to be true")
	}
	if s.Size != 4096 {
		t.Errorf("expected Size 4096, got %d", s.Size)
	}
	if s.Type != "fq" {
		t.Errorf("expected Type 'fq', got %q", s.Type)
	}
	if !s.Hashing {
		t.Error("expected Hashing to be true")
	}
	if s.MaxCachedPrefixes != 128 {
		t.Errorf("expected MaxCachedPrefixes 128, got %d", s.MaxCachedPrefixes)
	}
}

func TestWithSize(t *testing.T) {
	s := DefaultStrategy()
	WithSize(8192)(&s)

	if s.Size != 8192 {
		t.Errorf("expected Size 8192, got %d", s.Size)
	}
}

func TestWithType(t *testing.T) {
	s := DefaultStrategy()
	WithType("q4_0")(&s)

	if s.Type != "q4_0" {
		t.Errorf("expected Type 'q4_0', got %q", s.Type)
	}
}

func TestWithHashing(t *testing.T) {
	s := DefaultStrategy()
	WithHashing(false)(&s)

	if s.Hashing {
		t.Error("expected Hashing to be false")
	}
}

func TestHashPrefix(t *testing.T) {
	messages := []string{"Hello", "World"}
	h1 := HashPrefix(messages)
	h2 := HashPrefix(messages)

	if h1 == "" {
		t.Error("expected non-empty hash")
	}
	if h1 != h2 {
		t.Error("same input should produce same hash")
	}

	// Different input should produce different hash
	h3 := HashPrefix([]string{"Hello", "Universe"})
	if h1 == h3 {
		t.Error("different input should produce different hash")
	}

	// Empty messages should still produce a valid hash
	h4 := HashPrefix([]string{})
	if h4 == "" {
		t.Error("expected non-empty hash for empty messages")
	}
}

func TestEstimateCacheNeeded(t *testing.T) {
	s := EstimateCacheNeeded(1000)

	if !s.Enabled {
		t.Error("expected Enabled to be true")
	}
	if s.Size != 1200 { // 1000 + 20% headroom
		t.Errorf("expected Size 1200, got %d", s.Size)
	}
	if s.Type != "fq" {
		t.Errorf("expected Type 'fq', got %q", s.Type)
	}

	// Small prefix should have minimum cache size
	s2 := EstimateCacheNeeded(100)
	if s2.Size < 1024 {
		t.Errorf("expected minimum Size 1024, got %d", s2.Size)
	}
}

func TestComputeHitRateEstimate(t *testing.T) {
	prefixes := []string{"hello world", "good morning", "hi there"}
	counts := []int{10, 5, 3}

	// Cache larger than all prefixes — should be 100% hit rate
	rate := ComputeHitRateEstimate(prefixes, counts, 100)
	if rate != 1.0 {
		t.Errorf("expected 1.0, got %f", rate)
	}

	// Cache smaller than some prefixes — partial hit
	rate = ComputeHitRateEstimate(prefixes, counts, 5)
	// len("hello world")=11 > 5, len("good morning")=14 > 5, len("hi there")=8 > 5 => 0 hits
	if rate != 0.0 {
		t.Errorf("expected 0.0 (all too long), got %f", rate)
	}

	// Empty prefixes
	rate = ComputeHitRateEstimate([]string{}, []int{}, 100)
	if rate != 0.0 {
		t.Errorf("expected 0.0 for empty input, got %f", rate)
	}

	// Zero cache size
	rate = ComputeHitRateEstimate(prefixes, counts, 0)
	if rate != 0.0 {
		t.Errorf("expected 0.0 for zero cache size, got %f", rate)
	}

	// Mixed: some fit, some don't
	prefixes2 := []string{"hi", "hello world"}
	counts2 := []int{10, 5}
	rate = ComputeHitRateEstimate(prefixes2, counts2, 3) // "hi" (2 chars) fits, "hello world" (11) doesn't
	expected2 := float64(10) / float64(15)
	if rate != expected2 {
		t.Errorf("expected %f, got %f", expected2, rate)
	}
}

func TestCacheStrategyValidate(t *testing.T) {
	tests := []struct {
		name    string
		strategy CacheStrategy
		wantErr bool
	}{
		{
			name: "valid fq",
			strategy: CacheStrategy{Enabled: true, Size: 4096, Type: "fq", Hashing: true, MaxCachedPrefixes: 128},
			wantErr: false,
		},
		{
			name: "valid q4_0",
			strategy: CacheStrategy{Enabled: true, Size: 2048, Type: "q4_0", Hashing: false, MaxCachedPrefixes: 64},
			wantErr: false,
		},
		{
			name: "valid q8_0",
			strategy: CacheStrategy{Enabled: true, Size: 2048, Type: "q8_0", Hashing: false, MaxCachedPrefixes: 64},
			wantErr: false,
		},
		{
			name: "valid iq2_xs",
			strategy: CacheStrategy{Enabled: true, Size: 1024, Type: "iq2_xs", Hashing: false, MaxCachedPrefixes: 32},
			wantErr: false,
		},
		{
			name: "invalid type",
			strategy: CacheStrategy{Enabled: true, Size: 4096, Type: "invalid", Hashing: true, MaxCachedPrefixes: 128},
			wantErr: true,
		},
		{
			name: "zero size",
			strategy: CacheStrategy{Enabled: true, Size: 0, Type: "fq", Hashing: true, MaxCachedPrefixes: 128},
			wantErr: true,
		},
		{
			name: "negative size",
			strategy: CacheStrategy{Enabled: true, Size: -100, Type: "fq", Hashing: true, MaxCachedPrefixes: 128},
			wantErr: true,
		},
		{
			name: "zero max prefixes",
			strategy: CacheStrategy{Enabled: true, Size: 4096, Type: "fq", Hashing: true, MaxCachedPrefixes: 0},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.strategy.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v; wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestCacheStrategyValidate_EmptyType(t *testing.T) {
	s := CacheStrategy{Enabled: true, Size: 4096, Type: "", Hashing: true, MaxCachedPrefixes: 128}
	err := s.Validate()
	if err == nil {
		t.Error("expected error for empty type")
	}
}
