package main

import (
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
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
		errors = append(errors, fmt.Sprintf("type is required"))
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
		Temperature:    float32Ptr(p.Temperature),
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

// float64Ptr is a helper to create a *float64.
func float64Ptr(f float64) *float64 {
	return &f
}

// boolPtr is a helper to create a *bool.
func boolPtr(b bool) *bool {
	return &b
}

// ListProviders lists all configured LLM providers.
func (s *server) ListProviders(c echo.Context) error {
	responses := make([]*protocol.ProviderResponse, 0, len(s.cfg.System.LlmProviders))
	for i := range s.cfg.System.LlmProviders {
		responses = append(responses, toProviderResponse(&s.cfg.System.LlmProviders[i]))
	}
	return c.JSON(http.StatusOK, responses)
}

// CreateProvider adds a new LLM provider to the system configuration.
func (s *server) CreateProvider(c echo.Context) error {
	var req protocol.ProviderCreateRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body: " + err.Error()})
	}

	// Validate required fields.
	if req.Name == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "name is required"})
	}
	if req.Type == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "type is required"})
	} else if !isValidProviderType(string(req.Type)) {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("invalid type %q; must be one of: openai, anthropic, ollama, gemini, llamacpp", req.Type),
		})
	}
	if req.Endpoint == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "endpoint is required"})
	}

	// Check for duplicate name (case-insensitive).
	if findProviderByName(s.cfg.System, req.Name) != nil {
		return c.JSON(http.StatusConflict, map[string]string{
			"error": fmt.Sprintf("provider with name %q already exists", req.Name),
		})
	}

	// Build the provider config.
	prov := config.ProviderConfig{
		Name:      req.Name,
		Type:      string(req.Type),
		Endpoint:  req.Endpoint,
		Model:     derefString(req.Model),
		Temperature: derefFloat32(req.Temperature),
		MaxTokens: derefInt(req.MaxTokens),
		ApiKey:    derefString(req.ApiKey),
	}

	if req.Extra != nil {
		prov.Extra = *req.Extra
	}

	// Validate the provider.
	if errs := ProviderValidator(&prov); len(errs) > 0 {
		return c.JSON(http.StatusBadRequest, map[string]interface{}{
			"error": strings.Join(errs, "; "),
		})
	}

	s.cfg.System.LlmProviders = append(s.cfg.System.LlmProviders, prov)

	if err := s.cfg.Validate(); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
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

	return c.JSON(http.StatusCreated, toProviderResponse(&prov))
}

// GetProvider returns details for a single provider by name.
func (s *server) GetProvider(c echo.Context, name string) error {
	prov := findProviderByName(s.cfg.System, name)
	if prov == nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "provider not found: " + name})
	}
	return c.JSON(http.StatusOK, toProviderResponse(prov))
}

// UpdateProvider updates an existing provider's configuration.
func (s *server) UpdateProvider(c echo.Context, name string) error {
	var req protocol.ProviderUpdateRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body: " + err.Error()})
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
		return c.JSON(http.StatusNotFound, map[string]string{"error": "provider not found: " + name})
	}

	// Apply partial updates.
	prov := &s.cfg.System.LlmProviders[idx]

	if req.Type != nil {
		if !isValidProviderType(string(*req.Type)) {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"error": fmt.Sprintf("invalid type %q; must be one of: openai, anthropic, ollama, gemini, llamacpp", *req.Type),
			})
		}
		prov.Type = string(*req.Type)
	}
	if req.Endpoint != nil {
		prov.Endpoint = string(*req.Endpoint)
	}
	if req.Model != nil {
		prov.Model = string(*req.Model)
	}
	if req.Temperature != nil {
		prov.Temperature = float64(*req.Temperature)
	}
	if req.MaxTokens != nil {
		prov.MaxTokens = int(*req.MaxTokens)
	}
	if req.ApiKey != nil {
		prov.ApiKey = string(*req.ApiKey)
	}
	if req.Extra != nil {
		prov.Extra = *req.Extra
	}

	// Validate the updated provider.
	if errs := ProviderValidator(prov); len(errs) > 0 {
		return c.JSON(http.StatusBadRequest, map[string]interface{}{
			"error": strings.Join(errs, "; "),
		})
	}

	if err := s.cfg.Validate(); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
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

	return c.JSON(http.StatusOK, toProviderResponse(prov))
}

// DeleteProvider removes a provider by name. Cannot delete the last remaining one.
func (s *server) DeleteProvider(c echo.Context, name string) error {
	idx := -1
	for i := range s.cfg.System.LlmProviders {
		if strings.EqualFold(s.cfg.System.LlmProviders[i].Name, name) {
			idx = i
			break
		}
	}
	if idx == -1 {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "provider not found: " + name})
	}

	// Cannot delete the last remaining provider.
	if len(s.cfg.System.LlmProviders) <= 1 {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": "cannot delete the last remaining provider",
		})
	}

	removed := s.cfg.System.LlmProviders[idx]
	s.cfg.System.LlmProviders = append(s.cfg.System.LlmProviders[:idx], s.cfg.System.LlmProviders[idx+1:]...)

	if err := s.cfg.Validate(); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
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

	return c.NoContent(http.StatusNoContent)
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
