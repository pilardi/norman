package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/norman/norman/pkg/protocol"
	"github.com/urfave/cli/v3"
)

// providerEntry represents a single LLM provider configuration entry.
type providerEntry struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Endpoint    string   `json:"endpoint"`
	Model       *string  `json:"model,omitempty"`
	Temperature *float32 `json:"temperature,omitempty"`
	MaxTokens   *int     `json:"max_tokens,omitempty"`
}

// handleConfig shows or edits the current config.
func handleConfig(ctx context.Context, cmd *cli.Command) error {
	args := cmd.Args().Slice()

	if len(args) == 0 {
		return printConfig(ctx, nil, false)
	}

	switch args[0] {
	case "--set":
		return handleConfigSet(ctx, cmd.Args().Tail())
	case "--json":
		return printConfig(ctx, nil, true)
	default:
		return fmt.Errorf("unknown config option: %s (use --set key=value or --json)", args[0])
	}
}

// handleConfigJSON is the action for the --json flag.
func handleConfigJSON(ctx context.Context, cmd *cli.Command) error {
	return printConfig(ctx, nil, true)
}

// handleConfigSet handles the --set flag.
func handleConfigSet(ctx context.Context, args []string) error {
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

	if err := putConfig(ctx, update); err != nil {
		return fmt.Errorf("error updating config: %v", err)
	}
	fmt.Printf("Updated %s → %s\n", key, val)
	return nil
}

// printConfig outputs the current config (human-readable or JSON).
func printConfig(ctx context.Context, _ *cli.Command, asJSON bool) error {
	cfg, llmProviders, resolvedGateway, err := getConfig(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return err
	}

	if asJSON {
		output := map[string]interface{}{
			"server_port":        derefInt(cfg.ServerPort),
			"host":               strPtrVal(cfg.Host),
			"default_provider":   strPtrVal(cfg.DefaultProvider),
			"max_active_skills":  derefInt(cfg.Skills.MaxActive),
			"bell_enabled":       derefBool(cfg.Notify.BellEnabled),
			"rate_limit_per_min": derefInt(cfg.Notify.RateLimitPerMin),
			"openshell_address":  resolvedGateway,
		}
		if len(llmProviders) > 0 {
			providers := make([]map[string]interface{}, len(llmProviders))
			for i, p := range llmProviders {
				prov := map[string]interface{}{
					"name":        p.Name,
					"type":        p.Type,
					"endpoint":    p.Endpoint,
					"model":       strPtrVal(p.Model),
					"temperature": derefFloat32(p.Temperature),
					"max_tokens":  derefInt(p.MaxTokens),
				}
				providers[i] = prov
			}
			output["llm_providers"] = providers
		}
		data, _ := json.MarshalIndent(output, "", "  ")
		fmt.Println(string(data))
		return nil
	}

	fmt.Println("# Current Configuration (System)")
	fmt.Printf("  server_port:        %d\n", derefInt(cfg.ServerPort))
	fmt.Printf("  host:               %s\n", strPtrVal(cfg.Host))
	if cfg.DefaultProvider != nil {
		fmt.Printf("  default_provider:   %s\n", *cfg.DefaultProvider)
	}
	if cfg.Skills != nil {
		fmt.Printf("  max_active_skills:  %d\n", derefInt(cfg.Skills.MaxActive))
	}
	if cfg.Notify != nil {
		fmt.Printf("  bell_enabled:       %v\n", derefBool(cfg.Notify.BellEnabled))
		fmt.Printf("  rate_limit_per_min: %d\n", derefInt(cfg.Notify.RateLimitPerMin))
	}
	if resolvedGateway != "" {
		fmt.Printf("  openshell_address:  %s\n", resolvedGateway)
	} else if cfg.Openshell != nil && cfg.Openshell.Address != nil {
		fmt.Printf("  openshell_address:  %s\n", *cfg.Openshell.Address)
	}

	if len(llmProviders) > 0 {
		fmt.Println("  providers:")
		for i, p := range llmProviders {
			fmt.Printf("    [%d] name: %s\n", i+1, p.Name)
			fmt.Printf("        type:     %s\n", p.Type)
			fmt.Printf("        endpoint: %s\n", p.Endpoint)
			fmt.Printf("        model:    %s\n", strPtrVal(p.Model))
			fmt.Printf("        temp:     %.2f\n", derefFloat32(p.Temperature))
			fmt.Printf("        max_tokens: %d\n", derefInt(p.MaxTokens))
		}
	}
	return nil
}

// Helper: safely dereference a *int pointer for display.
// getConfig fetches the current system config from the server using the protocol client.
func getConfig(ctx context.Context) (*protocol.SystemConfig, []providerEntry, string, error) {
	client, err := getProtocolClient()
	if err != nil {
		return nil, nil, "", fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := client.GetConfig(ctx, nil)
	if err != nil {
		return nil, nil, "", fmt.Errorf("GET /api/config: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, "", fmt.Errorf("read config response body: %w", err)
	}

	// The server returns {"system": {...}, "openshell": "..."} — a flat wrapper.
	// Use a custom struct matching the actual wire format instead of ParseGetConfigResponse.
	var result struct {
		System       map[string]any   `json:"system"`
		OpenShell    string           `json:"openshell"`
		LlmProviders []map[string]any `json:"llm_providers,omitempty"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, nil, "", fmt.Errorf("decode config response: %w", err)
	}

	if result.System == nil {
		return nil, nil, "", fmt.Errorf("unexpected empty config response")
	}

	// Convert map to *protocol.SystemConfig using reflection-free field mapping.
	cfg := &protocol.SystemConfig{}
	if v, ok := result.System["ServerPort"]; ok && v != nil {
		if n, ok := v.(float64); ok {
			val := int(n)
			cfg.ServerPort = &val
		}
	}
	if v, ok := result.System["Host"]; ok && v != nil {
		if s, ok := v.(string); ok {
			cfg.Host = &s
		}
	}
	if v, ok := result.System["DefaultModel"]; ok && v != nil {
		if s, ok := v.(string); ok {
			cfg.DefaultModel = &s
		}
	}
	if v, ok := result.System["DefaultProvider"]; ok && v != nil {
		if s, ok := v.(string); ok {
			cfg.DefaultProvider = &s
		}
	}
	if v, ok := result.System["MaxTokens"]; ok && v != nil {
		if n, ok := v.(float64); ok {
			val := int(n)
			cfg.MaxTokens = &val
		}
	}
	if v, ok := result.System["Temperature"]; ok && v != nil {
		if f, ok := v.(float64); ok {
			f32 := float32(f)
			cfg.Temperature = &f32
		}
	}
	// Parse OpenShell config (nested PascalCase: "OpenShell":{"Address":"..."})
	if v, ok := result.System["OpenShell"]; ok {
		if m, ok := v.(map[string]any); ok {
			cfg.Openshell = &protocol.OpenShellConfig{}
			if addr, ok := m["Address"]; ok {
				if s, ok := addr.(string); ok {
					cfg.Openshell.Address = &s
				}
			}
		}
	}

	// Parse LlmProviders from top-level array (PascalCase)
	var llmProviders []providerEntry
	for _, m := range result.LlmProviders {
		pc := providerEntry{}
		if name, ok := m["Name"]; ok {
			if s, ok := name.(string); ok {
				pc.Name = s
			}
		}
		if typ, ok := m["Type"]; ok {
			if s, ok := typ.(string); ok {
				pc.Type = s
			}
		}
		if endpoint, ok := m["Endpoint"]; ok {
			if s, ok := endpoint.(string); ok {
				pc.Endpoint = s
			}
		}
		if model, ok := m["Model"]; ok {
			if s, ok := model.(string); ok {
				pc.Model = &s
			}
		}
		if temp, ok := m["Temperature"]; ok && temp != nil {
			if f, ok := temp.(float64); ok {
				f32 := float32(f)
				pc.Temperature = &f32
			}
		}
		if maxTokens, ok := m["MaxTokens"]; ok && maxTokens != nil {
			if n, ok := maxTokens.(float64); ok {
				val := int(n)
				pc.MaxTokens = &val
			}
		}
		llmProviders = append(llmProviders, pc)
	}

	// Parse Skills config (nested PascalCase: "Skills":{"DiscoveryPaths":[...],"MaxActive":3})
	if v, ok := result.System["Skills"]; ok {
		if m, ok := v.(map[string]any); ok {
			cfg.Skills = &protocol.SkillsConfig{}
			if maxActive, ok := m["MaxActive"]; ok && maxActive != nil {
				if n, ok := maxActive.(float64); ok {
					val := int(n)
					cfg.Skills.MaxActive = &val
				}
			}
			if paths, ok := m["DiscoveryPaths"]; ok {
				if arr, ok := paths.([]any); ok {
					cfg.Skills.DiscoveryPaths = &[]string{}
					for _, p := range arr {
						if s, ok := p.(string); ok {
							*cfg.Skills.DiscoveryPaths = append(*cfg.Skills.DiscoveryPaths, s)
						}
					}
				}
			}
		}
	}

	// Parse Notify config (nested PascalCase: "Notify":{"BellEnabled":true,"RateLimitPerMin":10})
	if v, ok := result.System["Notify"]; ok {
		if m, ok := v.(map[string]any); ok {
			cfg.Notify = &protocol.NotifyConfig{}
			if bell, ok := m["BellEnabled"]; ok {
				if b, ok := bell.(bool); ok {
					cfg.Notify.BellEnabled = &b
				}
			}
			if rateLimit, ok := m["RateLimitPerMin"]; ok && rateLimit != nil {
				if n, ok := rateLimit.(float64); ok {
					val := int(n)
					cfg.Notify.RateLimitPerMin = &val
				}
			}
		}
	}

	// Parse Logging config (nested PascalCase: "Logging":{"Verbose":false})
	if v, ok := result.System["Logging"]; ok {
		if m, ok := v.(map[string]any); ok {
			cfg.Logging = &struct {
				Level *string `json:"level,omitempty"`
			}{}
			if level, ok := m["Level"]; ok {
				if s, ok := level.(string); ok {
					cfg.Logging.Level = &s
				}
			}
		}
	}

	return cfg, llmProviders, result.OpenShell, nil
}

// putConfig sends a partial system config update to the server using the protocol client.
func putConfig(ctx context.Context, updates map[string]interface{}) error {
	client, err := getProtocolClient()
	if err != nil {
		return fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := client.PutConfig(ctx, updates)
	if err != nil {
		return fmt.Errorf("PUT /api/config: %w", err)
	}

	_, _ = protocol.ParsePutConfigResponse(resp)
	return nil
}
