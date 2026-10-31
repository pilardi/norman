package main

import (
	"fmt"
	"strconv"

	"github.com/norman/norman/pkg/protocol"
)

var (
	protocolClient    *protocol.Client
	protocolClientErr error
)

// getProtocolClient returns a shared protocol client, lazily initialized on first call.
// The client is created once using the configured server URL and reused for all requests.
func getProtocolClient() (*protocol.Client, error) {
	if protocolClient == nil {
		url := serverURL()
		protocolClient, protocolClientErr = protocol.NewClient(url)
	}
	return protocolClient, protocolClientErr
}

// strPtr returns a pointer to s if non-empty, otherwise nil.
func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// float32Ptr returns a pointer to f as a float32.
func float32Ptr(f float64) *float32 {
	v := float32(f)
	return &v
}

// intPtr returns a pointer to i if non-zero, otherwise nil.
func intPtr(i int) *int {
	if i == 0 {
		return nil
	}
	return &i
}

// strVal returns the dereferenced value of s, or empty string if nil.
func strVal(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// strPtrVal safely formats a pointer value for display.
func strPtrVal(t interface{}) string {
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

// parseFloat parses a string to float32, returning nil on failure.
func parseFloat(s string) *float32 {
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

// parseInt parses a string to int, returning nil on failure.
func parseInt(s string) *int {
	if s == "" {
		return nil
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return nil
	}
	return &v
}

// derefInt returns the value of i, or 0 if nil.
func derefInt(i *int) int {
	if i == nil {
		return 0
	}
	return *i
}

// derefBool returns the value of b, or false if nil.
func derefBool(b *bool) bool {
	if b == nil {
		return false
	}
	return *b
}

// derefFloat32 returns the value of f as float64, or 0 if nil.
func derefFloat32(f *float32) float64 {
	if f == nil {
		return 0
	}
	return float64(*f)
}
