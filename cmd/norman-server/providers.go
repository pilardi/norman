package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/norman/norman/pkg/config"
	"github.com/norman/norman/pkg/llm"
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
	} else if !protocol.ProviderConfigType(p.Type).Valid() {
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
	if (p.Type == string(protocol.ProviderConfigTypeOpenai) || p.Type == string(protocol.ProviderConfigTypeAnthropic)) && p.ApiKey == "" {
		errors = append(errors, "api_key is required for openai and anthropic types")
	}

	return errors
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
		ApiKey:       strPtr(p.ApiKey),
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
	} else if !protocol.ProviderConfigType(req.Type).Valid() {
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
		return c.JSON(http.StatusBadRequest, map[string]interface{}{
			"error": strings.Join(errs, "; "),
		})
	}

	s.cfg.System.LlmProviders = append(s.cfg.System.LlmProviders, prov)

	if err := s.cfg.Validate(); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}

	// Persist to disk.
	configPath := filepath.Join(s.cfg.System.BaseDirPath(), "config.yaml")
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
		if !protocol.ProviderConfigType(*req.Type).Valid() {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"error": fmt.Sprintf("invalid type %q; must be one of: openai, anthropic, ollama, gemini, llamacpp", *req.Type),
			})
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
		return c.JSON(http.StatusBadRequest, map[string]interface{}{
			"error": strings.Join(errs, "; "),
		})
	}

	if err := s.cfg.Validate(); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}

	// Persist to disk.
	configPath := filepath.Join(s.cfg.System.BaseDirPath(), "config.yaml")
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
	configPath := filepath.Join(s.cfg.System.BaseDirPath(), "config.yaml")
	if err := s.cfg.Save(configPath); err != nil {
		log.Printf("[WARN] failed to persist provider config: %v", err)
	}

	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr("Removed provider '" + removed.Name + "'"),
	})

	return c.NoContent(http.StatusNoContent)
}

// ListProviderModels queries the provider's API endpoint and returns available models.
// Supports OpenAI-compatible APIs (GET /v1/models), Ollama (/api/tags), and generic endpoints.
func (s *server) ListProviderModels(c echo.Context, name string) error {
	prov := findProviderByName(s.cfg.System, name)
	if prov == nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "provider not found: " + name})
	}

	models, err := llm.QueryProviderModels(prov)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "failed to query provider models: " + err.Error(),
		})
	}

	resp := &protocol.AvailableModelsResponse{
		Models:       &models,
		ProviderName: strPtr(prov.Name),
	}
	return c.JSON(http.StatusOK, resp)
}

// QueryProviderModels queries available models for a temporary provider configuration (no persistence).
// Accepts type, endpoint, and optional api_key from the request body.
func (s *server) QueryProviderModels(c echo.Context) error {
	var req protocol.QueryProviderModelsRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	if req.Type == "" || req.Endpoint == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": "type and endpoint are required",
		})
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

	models, err := llm.QueryProviderModels(tmpProv)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "failed to query provider models: " + err.Error(),
		})
	}

	resp := &protocol.AvailableModelsResponse{
		Models:       &models,
		ProviderName: strPtr("(temporary)"),
	}
	return c.JSON(http.StatusOK, resp)
}

// VerifyProvider tests connectivity and runs a quick completion against the provider's API.
func (s *server) VerifyProvider(c echo.Context, name string) error {
	prov := findProviderByName(s.cfg.System, name)
	if prov == nil {
		return c.JSON(http.StatusNotFound, map[string]string{
			"error": "provider not found",
		})
	}

	result := &protocol.VerifyResult{}

	// Step 1: Query available models (tests connectivity).
	models, err := llm.QueryProviderModels(prov)
	if err != nil {
		s := protocol.VerifyResultStatus("failed")
		m := protocol.VerifyResultModelListStatus("failure")
		cst := protocol.VerifyResultCompletionStatus("skipped")
		result.Status = &s
		result.ModelListStatus = &m
		result.ModelListError = strPtr(err.Error())
		result.CompletionStatus = &cst
	} else {
		s := protocol.VerifyResultStatus("ok")
		m := protocol.VerifyResultModelListStatus("success")
		result.Status = &s
		result.ModelListStatus = &m
		modelCount := len(models)
		result.ModelCount = &modelCount
	}

	// Step 2: Quick completion test (only if model list succeeded).
	if result.ModelListStatus != nil && *result.ModelListStatus == "success" && prov.Model != "" {
		comp, compErr := quickCompletion(prov)
		if compErr != nil {
			cst := protocol.VerifyResultCompletionStatus("failure")
			result.CompletionStatus = &cst
			result.CompletionError = strPtr(compErr.Error())
			if result.Status != nil && *result.Status == "ok" {
				f := protocol.VerifyResultStatus("failed")
				result.Status = &f
			}
		} else {
			cst := protocol.VerifyResultCompletionStatus("success")
			result.CompletionStatus = &cst
			tokens := comp.Usage.TotalTokens
			result.TokensUsed = &tokens
		}
	}

	statusStr := ""
	if result.Status != nil {
		statusStr = string(*result.Status)
	}

	var httpStatus int
	if statusStr == "failed" {
		httpStatus = http.StatusInternalServerError
	} else {
		httpStatus = http.StatusOK
	}
	return c.JSON(httpStatus, protocol.VerifyResponse{
		Status:       (*protocol.VerifyResponseStatus)(strPtr(statusStr)),
		Details:      result,
		ProviderName: strPtr(prov.Name),
	})
}

// VerifyProviderConfig tests connectivity and runs a quick completion against a proposed provider configuration (no persistence).
func (s *server) VerifyProviderConfig(c echo.Context) error {
	var cfg protocol.ProviderConfig
	if err := c.Bind(&cfg); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	// Validate required fields.
	if cfg.Name == "" || cfg.Type == "" || cfg.Endpoint == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": "name, type, and endpoint are required",
		})
	}

	// Convert protocol.ProviderConfig to config.ProviderConfig for LLM SDK calls.
	apiKeyVal := ""
	if cfg.ApiKey != nil {
		apiKeyVal = *cfg.ApiKey
	}
	tmpProv := &config.ProviderConfig{
		Name:     cfg.Name,
		Type:     string(cfg.Type),
		Endpoint: cfg.Endpoint,
		Model:    "",
		ApiKey:   apiKeyVal,
		Temperature: func() float64 {
			v := 0.7
			if cfg.Temperature != nil {
				v = float64(*cfg.Temperature)
			}
			return v
		}(),
		MaxTokens: func() int {
			if cfg.MaxTokens != nil {
				return *cfg.MaxTokens
			}
			return 0
		}(),
	}

	result := &protocol.VerifyResult{}

	// Step 1: Query available models (tests connectivity).
	models, err := llm.QueryProviderModels(tmpProv)
	if err != nil {
		s := protocol.VerifyResultStatus("failed")
		m := protocol.VerifyResultModelListStatus("failure")
		cst := protocol.VerifyResultCompletionStatus("skipped")
		result.Status = &s
		result.ModelListStatus = &m
		result.ModelListError = strPtr(err.Error())
		result.CompletionStatus = &cst
	} else {
		s := protocol.VerifyResultStatus("ok")
		m := protocol.VerifyResultModelListStatus("success")
		result.Status = &s
		result.ModelListStatus = &m
		modelCount := len(models)
		result.ModelCount = &modelCount
	}

	// Step 2: Quick completion test (only if model list succeeded and a model is specified).
	if result.ModelListStatus != nil && *result.ModelListStatus == "success" && cfg.Model != nil {
		tmpProv.Model = *cfg.Model
		comp, compErr := quickCompletion(tmpProv)
		if compErr != nil {
			cst := protocol.VerifyResultCompletionStatus("failure")
			result.CompletionStatus = &cst
			result.CompletionError = strPtr(compErr.Error())
			if result.Status != nil && *result.Status == "ok" {
				f := protocol.VerifyResultStatus("failed")
				result.Status = &f
			}
		} else {
			cst := protocol.VerifyResultCompletionStatus("success")
			result.CompletionStatus = &cst
			tokens := comp.Usage.TotalTokens
			result.TokensUsed = &tokens
		}
	}

	statusStr := ""
	if result.Status != nil {
		statusStr = string(*result.Status)
	}

	var httpStatus int
	if statusStr == "failed" {
		httpStatus = http.StatusInternalServerError
	} else {
		httpStatus = http.StatusOK
	}
	return c.JSON(httpStatus, protocol.VerifyResponse{
		Status:       (*protocol.VerifyResponseStatus)(strPtr(statusStr)),
		Details:      result,
		ProviderName: strPtr(cfg.Name),
	})
}

// quickCompletion sends a minimal completion request to test the provider's inference.
func quickCompletion(p *config.ProviderConfig) (*llm.CompletionResponse, error) {
	client := llm.NewClientWithProvider(p)
	resp, err := client.Complete(context.Background(), llm.CompletionRequest{
		Messages: []llm.Message{
			{Role: "system", Content: "You are a helpful assistant. Reply with exactly 'OK' and nothing else."},
			{Role: "user", Content: "Say OK"},
		},
		Temperature: 0.1,
		MaxTokens:   10,
	})
	if err != nil {
		return nil, fmt.Errorf("completion failed: %w", err)
	}
	return resp, nil
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
