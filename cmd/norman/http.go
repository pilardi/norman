package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/norman/norman/pkg/config"
)

// serverURL returns the configured server URL (with fallback).
// Reads directly from local config file to avoid circular dependency with getConfig().
func serverURL() string {
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

// urlPathEscape safely escapes a name for URL path segments.
func urlPathEscape(s string) string {
	return strings.ReplaceAll(s, "/", "%2F")
}

// logRequest logs a debug message when verbose mode is enabled.
func logRequest(method, url string) {
	fmt.Fprintf(os.Stderr, "[debug] %s %s\n", method, url)
}

// httpGet performs a GET request to the server and returns JSON bytes.
func httpGet(path string) ([]byte, error) {
	url := serverURL() + path
	if verbose {
		logRequest("GET", url)
	}
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("HTTP GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}

// httpPostJSON performs a POST request with JSON body and returns the response body.
// httpPutJSON performs a PUT request with JSON body and returns the response body.
var _ = httpPutJSON

func httpPutJSON(path string, body interface{}) ([]byte, error) {
	url := serverURL() + path
	if verbose {
		logRequest("PUT", url)
	}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal body: %w", err)
	}

	req, err := http.NewRequestWithContext(context.Background(), "PUT", url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("PUT %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(bodyBytes))
	}

	return io.ReadAll(resp.Body)
}
