package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/norman/norman/pkg/config"
)

// handleConfig shows or edits the current config.
func handleConfig(args []string) {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Println(`config — Show or edit configuration

Usage: norman config [--set key=value | --json]

Options:
  --set     Set a config value (e.g., --set server_port=9090)
  --json    Output as machine-readable JSON

Config is managed by the server at ~/.config/norman/config.yaml.
Secrets are stored as ${secret:...} placeholders and resolved by OpenShell.`)
		return
	}

	if len(args) > 0 && args[0] == "--set" {
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: norman config --set key=value")
			os.Exit(1)
		}
		parts := strings.SplitN(args[1], "=", 2)
		if len(parts) != 2 {
			fmt.Fprintf(os.Stderr, "Invalid format: %s (expected key=value)\n", args[1])
			os.Exit(1)
		}
		key, val := parts[0], parts[1]

		// Build partial update map
		update := make(map[string]interface{})
		switch key {
		case "default_model":
			update["default_model"] = val
		case "server_port":
			var port int
			if n, err := fmt.Sscanf(val, "%d", &port); n != 1 || err != nil {
				fmt.Fprintf(os.Stderr, "Invalid server_port value: %s\n", val)
				os.Exit(1)
			}
			update["server_port"] = port
		case "max_tokens":
			var t int
			if n, err := fmt.Sscanf(val, "%d", &t); n != 1 || err != nil {
				fmt.Fprintf(os.Stderr, "Invalid max_tokens value: %s\n", val)
				os.Exit(1)
			}
			update["max_tokens"] = t
		case "temperature":
			var t float64
			if n, err := fmt.Sscanf(val, "%f", &t); n != 1 || err != nil {
				fmt.Fprintf(os.Stderr, "Invalid temperature value: %s\n", val)
				os.Exit(1)
			}
			update["temperature"] = t
		case "logging.verbose":
			// Verbose is a client-side toggle (-v/--verbose); not shared with server.
			fmt.Printf("Verbose mode: %s (client-local, not persisted to server)\n", val)
			return
		case "openshell_address":
			update["openshell_address"] = val
		default:
			fmt.Fprintf(os.Stderr, "Unknown config key: %s\n", key)
			os.Exit(1)
		}

		// POST update to server
		if err := putConfig(update); err != nil {
			fmt.Fprintf(os.Stderr, "Error updating config: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Updated %s → %s\n", key, val)
		return
	}

	if len(args) > 0 && args[0] == "--json" {
		cfg, resolvedGateway, err := getConfig()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error fetching config from server: %v\n", err)
			os.Exit(1)
		}

		// Output snake_case for consistency with CLI display.
		output := map[string]interface{}{
			"server_port":        cfg.ServerPort,
			"host":               cfg.Host,
			"server_url":         cfg.ServerURL,
			"max_active_skills":  cfg.Skills.MaxActive,
			"bell_enabled":       cfg.Notify.BellEnabled,
			"rate_limit_per_min": cfg.Notify.RateLimitPerMin,
			"openshell_address":  resolvedGateway,
		}
		data, _ := json.MarshalIndent(output, "", "  ")
		fmt.Println(string(data))
		return
	}

	// Default: show current config (human-readable)
	cfg, resolvedGateway, err := getConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error fetching config from server: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("# Current Configuration (System)")
	fmt.Printf("  server_url:       %s\n", cfg.ServerURL)
	fmt.Printf("  server_port:      %d\n", cfg.ServerPort)
	fmt.Printf("  host:             %s\n", cfg.Host)
	fmt.Printf("  max_active_skills: %d\n", cfg.Skills.MaxActive)
	fmt.Printf("  bell_enabled:     %v\n", cfg.Notify.BellEnabled)
	fmt.Printf("  rate_limit_per_min: %d\n", cfg.Notify.RateLimitPerMin)
	// Show the resolved gateway address (from server-side resolution, not local config).
	if resolvedGateway != "" {
		fmt.Printf("  openshell_address: %s\n", resolvedGateway)
	} else if cfg.OpenShell.Address != "" {
		fmt.Printf("  openshell_address: %s\n", cfg.OpenShell.Address)
	}

	// Note: workspace overrides are managed server-side; client cannot read them here.
}

// getConfig fetches the current system config from the server.
func getConfig() (*config.SystemConfig, string, error) {
	resp, err := http.Get(serverURL() + "/api/config")
	if err != nil {
		return nil, "", fmt.Errorf("GET /api/config: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, "", fmt.Errorf("GET /api/config returned %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		System    *config.SystemConfig `json:"system"`
		OpenShell string               `json:"openshell"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, "", fmt.Errorf("decode config response: %w", err)
	}

	return result.System, result.OpenShell, nil
}

// putConfig sends a partial system config update to the server.
func putConfig(updates map[string]interface{}) error {
	body, err := json.Marshal(updates)
	if err != nil {
		return fmt.Errorf("marshal updates: %w", err)
	}

	req, err := http.NewRequest("PUT", serverURL()+"/api/config", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create PUT request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("PUT /api/config: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("PUT /api/config returned %d: %s", resp.StatusCode, string(data))
	}

	return nil
}
