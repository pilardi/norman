package main

import (
	"fmt"
	"strings"

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
