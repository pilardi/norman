// Package llamacpp provides KV-cache optimization utilities for llama.cpp prompt caching.
package llamacpp

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// CacheStrategy describes how prompt caching is configured and optimized.
type CacheStrategy struct {
	// Enabled controls whether server-side KV cache persistence is active.
	Enabled bool

	// Size is the maximum number of tokens the KV cache can hold (n_ctx).
	// The server uses this to bound memory allocation for cached states.
	Size int

	// Type selects the precision/quantization of stored KV entries.
	// "fq" = full precision (best accuracy, highest memory)
	// "q4_0" = 4-bit quantized (~4x less memory, slight accuracy loss)
	// "q8_0" = 8-bit quantized (~2x less memory, minimal accuracy loss)
	Type string

	// Hashing enables content-based prefix hashing for cache lookup.
	// When true, the server hashes prompt prefixes and maps them to saved KV states.
	Hashing bool

	// MaxCachedPrefixes is the maximum number of distinct prefix hashes retained.
	// Larger values improve hit rate but consume more RAM for the hash table.
	MaxCachedPrefixes int
}

// DefaultStrategy returns a CacheStrategy optimized for typical agent workloads
// where prompts share common system instructions and conversation history.
func DefaultStrategy() CacheStrategy {
	return CacheStrategy{
		Enabled:           true,
		Size:              4096, // 4k tokens — sufficient for most repeated prompts
		Type:              "fq", // full precision for accuracy-critical generation
		Hashing:           true,
		MaxCachedPrefixes: 128, // retain up to 128 distinct prefix hashes
	}
}

// WithSize returns a functional option that sets cache size.
func WithSize(size int) func(*CacheStrategy) {
	return func(s *CacheStrategy) { s.Size = size }
}

// WithType returns a functional option that sets cache type.
func WithType(typ string) func(*CacheStrategy) {
	return func(s *CacheStrategy) { s.Type = typ }
}

// WithHashing returns a functional option to enable/disable prefix hashing.
func WithHashing(enabled bool) func(*CacheStrategy) {
	return func(s *CacheStrategy) { s.Hashing = enabled }
}

// HashPrefix computes a SHA-256 hash of the concatenated prompt text,
// suitable for use as a cache-key when the server supports content hashing.
func HashPrefix(messages []string) string {
	combined := ""
	for _, m := range messages {
		combined += m + "\n"
	}
	h := sha256.Sum256([]byte(combined))
	return hex.EncodeToString(h[:])
}

// EstimateCacheNeeded estimates the minimum KV cache size (in tokens) needed
// to achieve a target hit rate for a given workload pattern.
//
// This is a heuristic: it assumes the longest repeated prefix determines
// the minimum cache size required for 100% hit rate on that prefix.
// For production tuning, monitor usage.cached_tokens vs prompt_tokens.
func EstimateCacheNeeded(maxPrefixTokens int) CacheStrategy {
	// Add 20% headroom for tokenization variance and metadata tokens
	size := maxPrefixTokens + maxPrefixTokens/5
	if size < 1024 {
		size = 1024 // minimum useful cache
	}

	return CacheStrategy{
		Enabled:           true,
		Size:              size,
		Type:              "fq",
		Hashing:           true,
		MaxCachedPrefixes: 64,
	}
}

// ComputeHitRateEstimate estimates the expected cache hit rate given a set of
// observed prompt prefixes and their frequencies. This is useful for pre-flight
// tuning before deploying to production.
//
// Arguments:
//   - prefixes: list of unique prompt prefix strings
//   - counts: frequency each prefix appears (same length as prefixes)
//   - cacheSize: the configured KV cache size in tokens
func ComputeHitRateEstimate(prefixes []string, counts []int, cacheSize int) float64 {
	if len(prefixes) == 0 || cacheSize <= 0 {
		return 0.0
	}

	// Find the longest prefix (in approximate token count, assuming ~1 char ≈ 1 token)
	longest := 0
	for _, p := range prefixes {
		if len(p) > longest {
			longest = len(p)
		}
	}

	if longest <= cacheSize {
		return 1.0 // all prefixes fit — theoretical 100% hit rate
	}

	// Partial hit: count prefixes that fit within cache size
	totalRequests := 0
	hitRequests := 0
	for i, p := range prefixes {
		requests := counts[i]
		totalRequests += requests
		if len(p) <= cacheSize {
			hitRequests += requests
		}
	}

	if totalRequests == 0 {
		return 0.0
	}
	return float64(hitRequests) / float64(totalRequests)
}

// Validate checks the CacheStrategy for consistency and returns errors.
func (s *CacheStrategy) Validate() error {
	validTypes := map[string]bool{
		"fq": true, "q4_0": true, "q8_0": true, "iq2_xs": true,
	}

	if s.Size <= 0 {
		return fmt.Errorf("cache size must be > 0, got %d", s.Size)
	}
	if !validTypes[s.Type] {
		return fmt.Errorf("cache type must be one of fq, q4_0, q8_0, iq2_xs, got %q", s.Type)
	}
	if s.MaxCachedPrefixes <= 0 {
		return fmt.Errorf("max_cached_prefixes must be > 0, got %d", s.MaxCachedPrefixes)
	}

	return nil
}
