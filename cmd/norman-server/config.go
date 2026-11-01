package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/norman/norman/pkg/config"
	"github.com/norman/norman/pkg/env"
	"github.com/norman/norman/pkg/protocol"
)

// GetConfig returns the system config (with unresolved secret placeholders).
// If workspace_id query param is provided, returns merged per-workspace config.
func (s *server) GetConfig(w http.ResponseWriter, r *http.Request, params protocol.GetConfigParams) {
	var workspaceID string
	if params.WorkspaceId != nil {
		workspaceID = *params.WorkspaceId
	}

	if workspaceID != "" {
		if ws, ok := s.cfg.Workspaces[workspaceID]; ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"system":    s.cfg.System,
				"workspace": ws,
				"merged":    s.cfg.MergeWorkspaceConfig(workspaceID, ws),
				"openshell": resolveGatewayAddress(),
			})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "workspace not found"})
		return
	}

	result := make(map[string]interface{})
	cfgCopy := *s.cfg.System
	cfgCopy.Logging = config.LoggingConfig{Verbose: false}
	result["system"] = cfgCopy
	result["openshell"] = resolveGatewayAddress()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(result)
}

// PutConfig accepts a partial system config update and merges it into the server's config.
func (s *server) PutConfig(w http.ResponseWriter, r *http.Request) {
	var updates map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid JSON body"})
		return
	}

	if err := mergeUpdatesIntoSystem(s.cfg.System, updates); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	if err := s.cfg.Validate(); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	configPath := os.ExpandEnv("$HOME/.norman/config.yaml")
	if err := s.cfg.Save(configPath); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "failed to save config"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "config updated",
		"system":  s.cfg.System,
	})
}

// mergeUpdatesIntoSystem applies partial key-value updates into a SystemConfig.
func mergeUpdatesIntoSystem(cfg *config.SystemConfig, updates map[string]interface{}) error {
	for k, v := range updates {
		switch k {
		case "server_port":
			if f, ok := v.(float64); ok {
				cfg.ServerPort = int(f)
			} else {
				return fmt.Errorf("server_port must be a number")
			}
		case "host":
			if s, ok := v.(string); ok {
				cfg.Host = s
			} else {
				return fmt.Errorf("host must be a string")
			}
		case "max_active_skills":
			if f, ok := v.(float64); ok {
				cfg.Skills.MaxActive = int(f)
			} else {
				return fmt.Errorf("max_active_skills must be a number")
			}
		case "bell_enabled":
			if b, ok := v.(bool); ok {
				cfg.Notify.BellEnabled = b
			} else {
				return fmt.Errorf("bell_enabled must be a boolean")
			}
		case "rate_limit_per_min":
			if f, ok := v.(float64); ok {
				cfg.Notify.RateLimitPerMin = int(f)
			} else {
				return fmt.Errorf("rate_limit_per_min must be a number")
			}
		case "openshell_address":
			if s, ok := v.(string); ok {
				cfg.OpenShell.Address = s
			} else {
				return fmt.Errorf("openshell_address must be a string")
			}
		case "default_provider":
			if s, ok := v.(string); ok {
				cfg.DefaultProvider = s
			} else {
				return fmt.Errorf("default_provider must be a string")
			}
		default:
			// Handle provider dot-path keys (e.g., "providers.0.name", "providers.1.temperature")
			if strings.HasPrefix(k, "providers.") {
				if err := applyProviderUpdate(cfg, k, v); err != nil {
					return err
				}
				continue
			}
			return fmt.Errorf("unknown config key: %s", k)
		}
	}
	return nil
}

// applyProviderUpdate applies a dot-path update to a specific provider.
// Expected format: "providers.<index>.<field>"
func applyProviderUpdate(cfg *config.SystemConfig, path string, value interface{}) error {
	parts := strings.SplitN(path, ".", 3)
	if len(parts) != 3 || parts[0] != "providers" {
		return fmt.Errorf("invalid provider path: %s", path)
	}

	// Parse index.
	var idx int
	if _, err := fmt.Sscanf(parts[1], "%d", &idx); err != nil {
		return fmt.Errorf("invalid provider index in %s: %v", path, err)
	}

	if idx < 0 || idx >= len(cfg.LlmProviders) {
		return fmt.Errorf("provider index %d out of range (have %d providers)", idx, len(cfg.LlmProviders))
	}

	prov := &cfg.LlmProviders[idx]
	field := parts[2]

	switch field {
	case "name":
		if s, ok := value.(string); ok {
			prov.Name = s
		} else {
			return fmt.Errorf("providers.%d.name must be a string", idx)
		}
	case "type":
		if s, ok := value.(string); ok {
			prov.Type = s
		} else {
			return fmt.Errorf("providers.%d.type must be a string", idx)
		}
	case "endpoint":
		if s, ok := value.(string); ok {
			prov.Endpoint = s
		} else {
			return fmt.Errorf("providers.%d.endpoint must be a string", idx)
		}
	case "model":
		if s, ok := value.(string); ok {
			prov.Model = s
		} else {
			return fmt.Errorf("providers.%d.model must be a string", idx)
		}
	case "temperature":
		if f, ok := value.(float64); ok {
			prov.Temperature = f
		} else {
			return fmt.Errorf("providers.%d.temperature must be a number", idx)
		}
	case "max_tokens":
		if f, ok := value.(float64); ok {
			prov.MaxTokens = int(f)
		} else {
			return fmt.Errorf("providers.%d.max_tokens must be a number", idx)
		}
	case "api_key":
		if s, ok := value.(string); ok {
			prov.ApiKey = s
		} else {
			return fmt.Errorf("providers.%d.api_key must be a string", idx)
		}
	default:
		return fmt.Errorf("unknown provider field %q", field)
	}

	return nil
}

// resolveGatewayAddress returns the currently-resolved OpenShell gateway address.
func resolveGatewayAddress() string {
	addr := os.Getenv("OPENSHELL_GATEWAY_ADDRESS")
	if addr != "" {
		return addr
	}
	return env.ResolveGatewayAddress()
}
