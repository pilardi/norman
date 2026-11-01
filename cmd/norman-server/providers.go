package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/norman/norman/pkg/config"
	"github.com/norman/norman/pkg/protocol"
)

// ProviderValidator validates a provider configuration against rules.
func ProviderValidator(p *config.ProviderConfig) []string {
	var errors []string

	if p.Name == "" {
		errors = append(errors, "name is required")
	}
	if p.Type == "" {
		errors = append(errors, "type is required")
	} else if !isValidProviderType(p.Type) {
		errors = append(errors, fmt.Sprintf("invalid type %q; must be one of: openai, anthropic, ollama, gemini, llamacpp", p.Type))
	}
	if p.Endpoint == "" {
		errors = append(errors, "endpoint is required")
	}
	if p.Model == "" {
		errors = append(errors, "model is required")
	}
	if p.Temperature < 0 || p.Temperature > 2 {
		errors = append(errors, fmt.Sprintf("temperature must be between 0 and 2, got %.2f", p.Temperature))
	}
	if p.MaxTokens < 1 {
		errors = append(errors, fmt.Sprintf("max_tokens must be >= 1, got %d", p.MaxTokens))
	}
	if (p.Type == "openai" || p.Type == "anthropic") && p.ApiKey == "" {
		errors = append(errors, "api_key is required for openai and anthropic types")
	}

	return errors
}

// isValidProviderType checks if a type string is one of the known provider types.
func isValidProviderType(t string) bool {
	switch t {
	case "openai", "anthropic", "ollama", "gemini", "llamacpp":
		return true
	default:
		return false
	}
}

// maskApiKey returns true if the api_key is non-empty (used for ProviderResponse).
func maskApiKey(key string) bool {
	return key != ""
}

// findProviderByName returns a pointer to the provider with the given name, or nil.
func findProviderByName(cfg *config.SystemConfig, name string) *config.ProviderConfig {
	for i := range cfg.LlmProviders {
		if strings.EqualFold(cfg.LlmProviders[i].Name, name) {
			return &cfg.LlmProviders[i]
		}
	}
	return nil
}

// toProviderResponse converts a ProviderConfig to a Protocol ProviderResponse.
func toProviderResponse(p *config.ProviderConfig) *protocol.ProviderResponse {
	resp := &protocol.ProviderResponse{
		Name:         strPtr(p.Name),
		Type:         (*protocol.ProviderResponseType)(strPtr(strings.ToLower(p.Type))),
		Endpoint:     strPtr(p.Endpoint),
		Model:        strPtr(p.Model),
		Temperature:  float32Ptr(p.Temperature),
		MaxTokens:    intPtr(p.MaxTokens),
		ApiKeyMasked: boolPtr(maskApiKey(p.ApiKey)),
	}
	if len(p.Extra) > 0 {
		resp.Extra = &p.Extra
	}
	return resp
}

// float32Ptr is a helper to create a *float32.
func float32Ptr(f float64) *float32 {
	v := float32(f)
	return &v
}

// boolPtr is a helper to create a *bool.
func boolPtr(b bool) *bool {
	return &b
}

// ListProviders lists all configured LLM providers.
func (s *server) ListProviders(w http.ResponseWriter, r *http.Request) {
	responses := make([]*protocol.ProviderResponse, 0, len(s.cfg.System.LlmProviders))
	for i := range s.cfg.System.LlmProviders {
		responses = append(responses, toProviderResponse(&s.cfg.System.LlmProviders[i]))
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(responses)
}

// CreateProvider adds a new LLM provider to the system configuration.
func (s *server) CreateProvider(w http.ResponseWriter, r *http.Request) {
	var req protocol.ProviderCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid request body: " + err.Error()})
		return
	}

	// Validate required fields.
	if req.Name == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "name is required"})
		return
	}
	if req.Type == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "type is required"})
		return
	} else if !isValidProviderType(string(req.Type)) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": fmt.Sprintf("invalid type %q; must be one of: openai, anthropic, ollama, gemini, llamacpp", req.Type),
		})
		return
	}
	if req.Endpoint == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "endpoint is required"})
		return
	}

	// Check for duplicate name (case-insensitive).
	if findProviderByName(s.cfg.System, req.Name) != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{
			"error": fmt.Sprintf("provider with name %q already exists", req.Name),
		})
		return
	}

	// Build the provider config.
	prov := config.ProviderConfig{
		Name:        req.Name,
		Type:        string(req.Type),
		Endpoint:    req.Endpoint,
		Model:       derefString(req.Model),
		Temperature: derefFloat32(req.Temperature),
		MaxTokens:   derefInt(req.MaxTokens),
		ApiKey:      derefString(req.ApiKey),
	}

	if req.Extra != nil {
		prov.Extra = *req.Extra
	}

	// Validate the provider.
	if errs := ProviderValidator(&prov); len(errs) > 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": strings.Join(errs, "; "),
		})
		return
	}

	s.cfg.System.LlmProviders = append(s.cfg.System.LlmProviders, prov)

	if err := s.cfg.Validate(); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	// Persist to disk.
	configPath := "/home/pablo/.norman/config.yaml"
	if err := s.cfg.Save(configPath); err != nil {
		log.Printf("[WARN] failed to persist provider config: %v", err)
	}

	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr("Added new LLM provider '" + prov.Name + "'"),
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(toProviderResponse(&prov))
}

// GetProvider returns details for a single provider by name.
func (s *server) GetProvider(w http.ResponseWriter, r *http.Request, name string) {
	prov := findProviderByName(s.cfg.System, name)
	if prov == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "provider not found: " + name})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(toProviderResponse(prov))
}

// UpdateProvider updates an existing provider's configuration.
func (s *server) UpdateProvider(w http.ResponseWriter, r *http.Request, name string) {
	var req protocol.ProviderUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid request body: " + err.Error()})
		return
	}

	// Find existing provider.
	idx := -1
	for i := range s.cfg.System.LlmProviders {
		if strings.EqualFold(s.cfg.System.LlmProviders[i].Name, name) {
			idx = i
			break
		}
	}
	if idx == -1 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "provider not found: " + name})
		return
	}

	// Apply partial updates.
	prov := &s.cfg.System.LlmProviders[idx]

	if req.Type != nil {
		if !isValidProviderType(string(*req.Type)) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{
				"error": fmt.Sprintf("invalid type %q; must be one of: openai, anthropic, ollama, gemini, llamacpp", *req.Type),
			})
			return
		}
		prov.Type = string(*req.Type)
	}
	if req.Endpoint != nil {
		prov.Endpoint = *req.Endpoint
	}
	if req.Model != nil {
		prov.Model = *req.Model
	}
	if req.Temperature != nil {
		prov.Temperature = float64(*req.Temperature)
	}
	if req.MaxTokens != nil {
		prov.MaxTokens = *req.MaxTokens
	}
	if req.ApiKey != nil {
		prov.ApiKey = *req.ApiKey
	}
	if req.Extra != nil {
		prov.Extra = *req.Extra
	}

	// Validate the updated provider.
	if errs := ProviderValidator(prov); len(errs) > 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": strings.Join(errs, "; "),
		})
		return
	}

	if err := s.cfg.Validate(); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	// Persist to disk.
	configPath := "/home/pablo/.norman/config.yaml"
	if err := s.cfg.Save(configPath); err != nil {
		log.Printf("[WARN] failed to persist provider config: %v", err)
	}

	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr("Updated provider '" + prov.Name + "'"),
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(toProviderResponse(prov))
}

// DeleteProvider removes a provider by name. Cannot delete the last remaining one.
func (s *server) DeleteProvider(w http.ResponseWriter, r *http.Request, name string) {
	idx := -1
	for i := range s.cfg.System.LlmProviders {
		if strings.EqualFold(s.cfg.System.LlmProviders[i].Name, name) {
			idx = i
			break
		}
	}
	if idx == -1 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "provider not found: " + name})
		return
	}

	// Cannot delete the last remaining provider.
	if len(s.cfg.System.LlmProviders) <= 1 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "cannot delete the last remaining provider",
		})
		return
	}

	removed := s.cfg.System.LlmProviders[idx]
	s.cfg.System.LlmProviders = append(s.cfg.System.LlmProviders[:idx], s.cfg.System.LlmProviders[idx+1:]...)

	if err := s.cfg.Validate(); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	// Persist to disk.
	configPath := "/home/pablo/.norman/config.yaml"
	if err := s.cfg.Save(configPath); err != nil {
		log.Printf("[WARN] failed to persist provider config: %v", err)
	}

	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr("Removed provider '" + removed.Name + "'"),
	})

	w.WriteHeader(http.StatusNoContent)
}

// ListProviderModels queries the provider's API endpoint and returns available models.
// Supports OpenAI-compatible APIs (GET /v1/models), Ollama (/api/tags), and generic endpoints.
func (s *server) ListProviderModels(w http.ResponseWriter, r *http.Request, name string) {
	prov := findProviderByName(s.cfg.System, name)
	if prov == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "provider not found: " + name})
		return
	}

	models, err := queryProviderModels(prov)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "failed to query provider models: " + err.Error(),
		})
		return
	}

	resp := &protocol.AvailableModelsResponse{
		Models:       &models,
		ProviderName: strPtr(prov.Name),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// QueryProviderModels queries available models for a temporary provider configuration (no persistence).
// Accepts type, endpoint, and optional api_key from the request body.
func (s *server) QueryProviderModels(w http.ResponseWriter, r *http.Request) {
	var req protocol.QueryProviderModelsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid request body"})
		return
	}

	if req.Type == "" || req.Endpoint == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "type and endpoint are required",
		})
		return
	}

	// Build a temporary ProviderConfig from the request body.
	apiKeyVal := ""
	if req.ApiKey != nil {
		apiKeyVal = *req.ApiKey
	}
	tmpProv := &config.ProviderConfig{
		Name:     "(temporary)",
		Type:     req.Type,
		Endpoint: req.Endpoint,
		ApiKey:   apiKeyVal,
		Model:    "",
	}

	models, err := queryProviderModels(tmpProv)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "failed to query provider models: " + err.Error(),
		})
		return
	}

	resp := &protocol.AvailableModelsResponse{
		Models:       &models,
		ProviderName: strPtr("(temporary)"),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// queryProviderModels queries the provider's API endpoint to list available models.
func queryProviderModels(p *config.ProviderConfig) ([]protocol.AvailableModelEntry, error) {
	var entries []protocol.AvailableModelEntry

	switch strings.ToLower(p.Type) {
	case "openai", "gemini":
		// OpenAI-compatible: GET /v1/models
		models, err := fetchOpenAIModels(p.Endpoint, p.ApiKey)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch models from %s: %w", p.Endpoint, err)
		}
		entries = models

	case "ollama":
		// Ollama: GET /api/tags
		models, err := fetchOllamaModels(p.Endpoint)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch models from %s: %w", p.Endpoint, err)
		}
		entries = models

	case "llamacpp":
		// llama.cpp exposes an OpenAI-compatible /v1/models endpoint.
		models, err := fetchOpenAIModels(p.Endpoint, "")
		if err != nil {
			return nil, fmt.Errorf("failed to fetch models from %s: %w", p.Endpoint, err)
		}
		entries = models

	default:
		return nil, fmt.Errorf("unsupported provider type %q for model discovery", p.Type)
	}

	return entries, nil
}

// fetchOpenAIModels queries an OpenAI-compatible endpoint's /v1/models.
func fetchOpenAIModels(endpoint, apiKey string) ([]protocol.AvailableModelEntry, error) {
	url := endpoint
	if !strings.HasSuffix(url, "/v1") {
		url = strings.TrimSuffix(url, "/") + "/v1"
	}
	url += "/models"

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	// Parse OpenAI-compatible response: {"object":"list","data":[{"id":"gpt-4o",...},...]}
	var openaiResp struct {
		Object string           `json:"object"`
		Data   []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &openaiResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	var entries []protocol.AvailableModelEntry
	for _, m := range openaiResp.Data {
		entry := protocol.AvailableModelEntry{}
		if id, ok := m["id"].(string); ok {
			entry.Id = &id
		}
		if ownedBy, ok := m["owned_by"].(string); ok {
			entry.OwnedBy = &ownedBy
		}
		// Use id as name if no separate display name exists
		var name string
		if n, ok := m["name"].(string); ok {
			name = n
		} else if entry.Id != nil {
			name = *entry.Id
		}
		entry.Name = &name
		entries = append(entries, entry)
	}

	return entries, nil
}

// fetchOllamaModels queries Ollama's /api/tags endpoint.
func fetchOllamaModels(endpoint string) ([]protocol.AvailableModelEntry, error) {
	url := strings.TrimSuffix(endpoint, "/") + "/api/tags"

	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	_ = resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	// Ollama format: {"models":[{"name":"llama3.1:8b","size":...},...]}
	var ollamaResp struct {
		Models []map[string]any `json:"models"`
	}
	if err := json.Unmarshal(body, &ollamaResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	var entries []protocol.AvailableModelEntry
	for _, m := range ollamaResp.Models {
		entry := protocol.AvailableModelEntry{}
		if name, ok := m["name"].(string); ok {
			entry.Id = &name
			entry.Name = strPtr(name)
		}
		entries = append(entries, entry)
	}

	return entries, nil
}

// --- Helper functions for safe pointer dereferencing ---

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func derefFloat32(p *float32) float64 {
	if p == nil {
		return 0
	}
	return float64(*p)
}

func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}
