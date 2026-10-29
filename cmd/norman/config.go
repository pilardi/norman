package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/norman/norman/pkg/config"
	"github.com/urfave/cli/v3"
)

// handleConfig shows or edits the current config.
func handleConfig(ctx context.Context, cmd *cli.Command) error {
	args := cmd.Args().Slice()

	if len(args) == 0 {
		return printConfig(nil, false)
	}

	switch args[0] {
	case "--set":
		return handleConfigSet(cmd.Args().Tail())
	case "--json":
		return printConfig(nil, true)
	default:
		return fmt.Errorf("unknown config option: %s (use --set key=value or --json)", args[0])
	}
}

// handleConfigSet handles the --set flag.
func handleConfigSet(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: norman config --set key=value")
	}
	parts := strings.SplitN(args[0], "=", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid format: %s (expected key=value)", args[0])
	}
	key, val := parts[0], parts[1]

	update := make(map[string]interface{})
	switch key {
	case "default_model":
		update["default_model"] = val
	case "server_port":
		port, err := strconv.Atoi(val)
		if err != nil {
			return fmt.Errorf("invalid server_port value: %s", val)
		}
		update["server_port"] = port
	case "max_tokens":
		t, err := strconv.Atoi(val)
		if err != nil {
			return fmt.Errorf("invalid max_tokens value: %s", val)
		}
		update["max_tokens"] = t
	case "temperature":
		t, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return fmt.Errorf("invalid temperature value: %s", val)
		}
		update["temperature"] = t
	case "logging.verbose":
		fmt.Printf("Verbose mode: %s (client-local, not persisted to server)\n", val)
		return nil
	case "openshell_address":
		update["openshell_address"] = val
	case "default_provider":
		update["default_provider"] = val
	default:
		if strings.HasPrefix(key, "providers.") {
			update[key] = val
		} else {
			return fmt.Errorf("unknown config key: %s", key)
		}
	}

	if err := putConfig(update); err != nil {
		return fmt.Errorf("error updating config: %v", err)
	}
	fmt.Printf("Updated %s → %s\n", key, val)
	return nil
}

// printConfig outputs the current config (human-readable or JSON).
func printConfig(_ *cli.Command, asJSON bool) error {
	cfg, resolvedGateway, err := getConfig()
	if err != nil {
		return fmt.Errorf("error fetching config from server: %v", err)
	}

	if asJSON {
		output := map[string]interface{}{
			"server_port":        cfg.ServerPort,
			"host":               cfg.Host,
			"server_url":         cfg.ServerURL,
			"default_provider":   cfg.DefaultProvider,
			"max_active_skills":  cfg.Skills.MaxActive,
			"bell_enabled":       cfg.Notify.BellEnabled,
			"rate_limit_per_min": cfg.Notify.RateLimitPerMin,
			"openshell_address":  resolvedGateway,
		}
		if len(cfg.LlmProviders) > 0 {
			providers := make([]map[string]interface{}, len(cfg.LlmProviders))
			for i, p := range cfg.LlmProviders {
				prov := map[string]interface{}{
					"name":       p.Name,
					"type":       p.Type,
					"endpoint":   p.Endpoint,
					"model":      p.Model,
					"temp":       p.Temperature,
					"max_tokens": p.MaxTokens,
				}
				if len(p.Extra) > 0 {
					prov["extra"] = p.Extra
				}
				providers[i] = prov
			}
			output["providers"] = providers
		}
		data, _ := json.MarshalIndent(output, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	fmt.Println("# Current Configuration (System)")
	fmt.Printf("  server_url:       %s\n", cfg.ServerURL)
	fmt.Printf("  server_port:      %d\n", cfg.ServerPort)
	fmt.Printf("  host:             %s\n", cfg.Host)
	if cfg.DefaultProvider != "" {
		fmt.Printf("  default_provider: %s\n", cfg.DefaultProvider)
	}
	fmt.Printf("  max_active_skills: %d\n", cfg.Skills.MaxActive)
	fmt.Printf("  bell_enabled:     %v\n", cfg.Notify.BellEnabled)
	fmt.Printf("  rate_limit_per_min: %d\n", cfg.Notify.RateLimitPerMin)
	if resolvedGateway != "" {
		fmt.Printf("  openshell_address: %s\n", resolvedGateway)
	} else if cfg.OpenShell.Address != "" {
		fmt.Printf("  openshell_address: %s\n", cfg.OpenShell.Address)
	}

	if len(cfg.LlmProviders) > 0 {
		fmt.Println("  providers:")
		for i, p := range cfg.LlmProviders {
			masked := " (key masked)"
			if p.ApiKey == "" {
				masked = ""
			}
			fmt.Printf("    [%d] name: %s\n", i+1, p.Name)
			fmt.Printf("        type:     %s\n", p.Type)
			fmt.Printf("        endpoint: %s\n", p.Endpoint)
			fmt.Printf("        model:    %s\n", p.Model)
			fmt.Printf("        temp:     %.2f\n", p.Temperature)
			fmt.Printf("        max_tokens: %d%s\n", p.MaxTokens, masked)
		}
	}
	return nil
}

// getConfig fetches the current system config from the server.
func getConfig() (*config.SystemConfig, string, error) {
	resp, err := http.Get(serverURL() + "/api/config")
	if err != nil {
		return nil, "", fmt.Errorf("GET /api/config: %w", err)
	}
	_ = resp.Body.Close()

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
	_ = resp.Body.Close()

	if resp.StatusCode >= 400 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("PUT /api/config returned %d: %s", resp.StatusCode, string(data))
	}

	return nil
}
