// Package client provides shared utilities for both REPL and TUI clients.
package client

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/norman/norman/pkg/config"
	"github.com/norman/norman/pkg/protocol"
)

var (
	ProtocolClient    *protocol.Client
	ProtocolClientErr error
)

// GetProtocolClient returns a shared protocol client, lazily initialized on first call.
// The client is created once using the configured server URL and reused for all requests.
func GetProtocolClient() (*protocol.Client, error) {
	if ProtocolClient == nil {
		url := ServerURL()
		ProtocolClient, ProtocolClientErr = protocol.NewClient(url)
	}
	return ProtocolClient, ProtocolClientErr
}

// ServerURL returns the configured server URL (with fallback).
// Reads directly from local config file to avoid circular dependency with getConfig().
func ServerURL() string {
	cfg, err := config.LoadDefault()
	if err != nil || cfg == nil || cfg.System == nil {
		return fmt.Sprintf("http://%s:%d", config.EmbeddedDefaults().System.Host, config.EmbeddedDefaults().System.ServerPort)
	}
	if cfg.System.ServerURL != "" {
		return cfg.System.ServerURL
	}
	def := config.EmbeddedDefaults()
	host := cfg.System.Host
	port := cfg.System.ServerPort
	if host == "" {
		host = def.System.Host
	}
	if port == 0 {
		port = def.System.ServerPort
	}
	return fmt.Sprintf("http://%s:%d", host, port)
}

// UrlPathEscape safely escapes a name for URL path segments.
func UrlPathEscape(s string) string {
	return strings.ReplaceAll(s, "/", "%2F")
}

// StrPtr returns a pointer to s if non-empty, otherwise nil.
func StrPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Float32Ptr returns a pointer to f as a float32.
func Float32Ptr(f float64) *float32 {
	v := float32(f)
	return &v
}

// IntPtr returns a pointer to i if non-zero, otherwise nil.
func IntPtr(i int) *int {
	if i == 0 {
		return nil
	}
	return &i
}

// StrVal returns the dereferenced value of s, or empty string if nil.
func StrVal(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// StrPtrVal safely formats a pointer value for display.
func StrPtrVal(t interface{}) string {
	switch v := t.(type) {
	case *protocol.ProviderResponseType:
		if v == nil {
			return ""
		}
		return string(*v)
	case *string:
		if v == nil {
			return ""
		}
		return *v
	default:
		return fmt.Sprintf("%v", t)
	}
}

// ParseFloat parses a string to float32, returning nil on failure.
func ParseFloat(s string) *float32 {
	if s == "" {
		return nil
	}
	v, err := strconv.ParseFloat(s, 32)
	if err != nil {
		return nil
	}
	v32 := float32(v)
	return &v32
}

// ParseInt parses a string to int, returning nil on failure.
func ParseInt(s string) *int {
	if s == "" {
		return nil
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return nil
	}
	return &v
}

// DerefInt returns the value of i, or 0 if nil.
func DerefInt(i *int) int {
	if i == nil {
		return 0
	}
	return *i
}

// DerefBool returns the value of b, or false if nil.
func DerefBool(b *bool) bool {
	if b == nil {
		return false
	}
	return *b
}

// DerefFloat32 returns the value of f as float64, or 0 if nil.
func DerefFloat32(f *float32) float64 {
	if f == nil {
		return 0
	}
	return float64(*f)
}

// ShortID truncates a session ID for display.
func ShortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
