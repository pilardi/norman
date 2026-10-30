package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/norman/norman/pkg/protocol"
	"github.com/urfave/cli/v3"
)

// handleProviders implements the `norman provider` subcommands.
// Usage: norman providers add <name> --type <type> --endpoint <url> [flags...]
//
//	norman providers list
//
// reservedExtraKeys are struct field names that must not appear in the extra map.
var reservedExtraKeys = map[string]struct{}{
	"name":           {},
	"type":           {},
	"endpoint":       {},
	"model":          {},
	"temperature":    {},
	"max_tokens":     {},
	"api_key_masked": {},
}

// parseExtra parses "key=val,key2=val2" into a map[string]interface{}, filtering
// out keys that conflict with struct fields (e.g. "name", "type").
func parseExtra(s string) map[string]interface{} {
	result := make(map[string]interface{})
	pairs := strings.Split(s, ",")
	for _, pair := range pairs {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		idx := strings.Index(pair, "=")
		if idx == -1 {
			key := strings.TrimSpace(pair)
			if _, ok := reservedExtraKeys[key]; !ok {
				result[key] = true
			}
		} else {
			key := strings.TrimSpace(pair[:idx])
			val := strings.TrimSpace(pair[idx+1:])
			if key != "" {
				if _, ok := reservedExtraKeys[key]; !ok {
					result[key] = val
				}
			}
		}
	}
	return result
}

// handleProvidersAdd adds a new LLM provider via POST /api/providers.
func handleProvidersAdd(ctx context.Context, cmd *cli.Command) error {
	name := cmd.Args().First()
	if name == "" {
		return fmt.Errorf("provider name is required")
	}

	req := map[string]interface{}{"name": name}

	if cmd.String("type") != "" {
		req["type"] = cmd.String("type")
	}
	if cmd.String("endpoint") != "" {
		req["endpoint"] = cmd.String("endpoint")
	}
	if cmd.String("model") != "" {
		req["model"] = cmd.String("model")
	}
	if cmd.Float64("temp") > 0 {
		req["temperature"] = cmd.Float64("temp")
	}
	if cmd.Int("max-tokens") > 0 {
		req["max_tokens"] = cmd.Int("max-tokens")
	}
	if cmd.String("api-key") != "" {
		req["api_key"] = cmd.String("api-key")
	}

	body, err := httpPostJSON("/api/providers", req)
	if err != nil {
		return fmt.Errorf("error creating provider: %v", err)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(body, &resp); err != nil {
		return fmt.Errorf("error parsing response: %v", err)
	}

	fmt.Printf("Provider '%s' created successfully.\n", name)
	printprovidersSummary(resp)
	return nil
}

// handleProvidersList lists all configured providers.
func handleProvidersList(ctx context.Context, cmd *cli.Command) error {
	body, err := httpGet("/api/providers")
	if err != nil {
		return fmt.Errorf("error fetching providers: %v", err)
	}

	var providers []map[string]interface{}
	if err := json.Unmarshal(body, &providers); err != nil {
		return fmt.Errorf("error parsing providers response: %v", err)
	}

	if len(providers) == 0 {
		fmt.Println("No providers configured.")
		return nil
	}

	fmt.Printf("\nConfigured LLM Providers (%d):\n", len(providers))
	fmt.Println(strings.Repeat("-", 70))
	for i, p := range providers {
		printprovidersSummary(p)
		if i < len(providers)-1 {
			fmt.Println(strings.Repeat("-", 70))
		}
	}
	fmt.Println()
	return nil
}

// handleProvidersGet shows details for a specific provider.
func handleProvidersGet(name string) error {
	body, err := httpGet("/api/providers/" + urlPathEscape(name))
	if err != nil {
		return fmt.Errorf("error fetching provider '%s': %v", name, err)
	}

	var provider map[string]interface{}
	if err := json.Unmarshal(body, &provider); err != nil {
		return fmt.Errorf("error parsing provider response: %v", err)
	}

	fmt.Printf("Provider '%s':\n", name)
	printprovidersSummary(provider)
	return nil
}

// handleProvidersUpdate updates an existing provider's fields.
func handleProvidersUpdate(ctx context.Context, cmd *cli.Command) error {
	name := cmd.Args().First()
	if name == "" {
		return fmt.Errorf("provider name is required")
	}

	req := map[string]interface{}{"name": name}

	if cmd.String("type") != "" {
		req["type"] = cmd.String("type")
	}
	if cmd.String("endpoint") != "" {
		req["endpoint"] = cmd.String("endpoint")
	}
	if cmd.String("model") != "" {
		req["model"] = cmd.String("model")
	}
	if cmd.Float64("temp") > 0 {
		req["temperature"] = cmd.Float64("temp")
	}
	if cmd.Int("max-tokens") > 0 {
		req["max_tokens"] = cmd.Int("max-tokens")
	}
	if cmd.String("api-key") != "" {
		req["api_key"] = cmd.String("api-key")
	}

	url := "/api/providers/" + urlPathEscape(name)
	body, err := httpPutJSON(url, req)
	if err != nil {
		return fmt.Errorf("error updating provider '%s': %v", name, err)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(body, &resp); err != nil {
		return fmt.Errorf("error parsing response: %v", err)
	}

	fmt.Printf("Provider '%s' updated successfully.\n", name)
	printprovidersSummary(resp)
	return nil
}

// handleProvidersDelete deletes a provider via DELETE /api/providers/{name}.
func handleProvidersDelete(name string) error {
	err := httpDelete("/api/providers/" + urlPathEscape(name))
	if err != nil {
		return fmt.Errorf("error deleting provider '%s': %v", name, err)
	}

	fmt.Printf("Provider '%s' removed.\n", name)
	return nil
}

// handleProvidersSetDefault sets the default provider via config update.
func handleProvidersSetDefault(name string) error {
	update := map[string]interface{}{
		"default_provider": name,
	}

	if err := putConfig(update); err != nil {
		return fmt.Errorf("error setting default provider '%s': %v", name, err)
	}

	fmt.Printf("Default provider set to '%s'.\n", name)
	return nil
}

// handleProvidersListModels lists available models for a provider.
func handleProvidersListModels(name string) error {
	body, err := httpGet("/api/providers/" + urlPathEscape(name) + "/models")
	if err != nil {
		return fmt.Errorf("error fetching models for provider '%s': %v", name, err)
	}

	var resp protocol.AvailableModelsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return fmt.Errorf("error parsing model response: %v", err)
	}

	models := []modelEntry{}
	if resp.Models != nil {
		for _, m := range *resp.Models {
			id := ""
			if m.Id != nil {
				id = *m.Id
			}
			nameVal := ""
			if m.Name != nil {
				nameVal = *m.Name
			}
			models = append(models, modelEntry{Id: id, Name: nameVal})
		}
	}

	if len(models) == 0 {
		fmt.Printf("\nNo models found for provider '%s'.\n", name)
		return nil
	}

	fmt.Printf("\nAvailable models for provider '%s' (%d found):\n\n", name, len(models))
	fmt.Printf("%-4s %-30s %s\n", "#", "ID", "Name")
	fmt.Println(strings.Repeat("-", 70))
	for i, m := range models {
		nameField := m.Name
		if nameField == "" {
			nameField = "-"
		}
		fmt.Printf("%-4d %-30s %s\n", i+1, m.Id, nameField)
	}
	fmt.Println()
	return nil
}

// handleProvidersAddInteractive runs an interactive prompt-based workflow for adding a provider.
var _ = handleProvidersAddInteractive

func handleProvidersAddInteractive() {
	scanner := bufio.NewScanner(os.Stdin)
	prompt := func(label string) string {
		fmt.Printf("%s: ", label)
		scanner.Scan()
		return strings.TrimSpace(scanner.Text())
	}

	name := prompt("Provider name")
	if name == "" {
		fmt.Fprintln(os.Stderr, "Error: provider name is required")
		os.Exit(1)
	}

	validTypes := []string{"openai", "anthropic", "ollama", "gemini", "llamacpp"}
	fmt.Printf("Valid types: %s\n", strings.Join(validTypes, ", "))
	providerType := prompt("Provider type")
	if providerType == "" {
		fmt.Fprintln(os.Stderr, "Error: type is required")
		os.Exit(1)
	}

	endpoint := prompt("Endpoint URL")
	if endpoint == "" {
		fmt.Fprintln(os.Stderr, "Error: endpoint is required")
		os.Exit(1)
	}

	apiKey := prompt("API key (optional)")

	// After entering endpoint and API key, fetch available models from the provider.
	models, err := fetchAvailableModels(endpoint, apiKey, providerType)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not fetch available models: %v\n", err)
		fmt.Println("You can enter a model name manually below.")
	}

	var model string
	if len(models) > 0 {
		fmt.Printf("\nAvailable models for this provider (%d found):\n", len(models))
		for i, m := range models {
			fmt.Printf("  %d. %s\n", i+1, m.Id)
		}
		model = prompt("Select a model number (or press Enter to skip)")
		if model != "" {
			idx, err := strconv.Atoi(model)
			if err != nil || idx < 1 || idx > len(models) {
				fmt.Fprintf(os.Stderr, "Error: invalid selection '%s'\n", model)
				os.Exit(1)
			}
			model = models[idx-1].Id
		}
	} else {
		model = prompt("Model (optional)")
	}

	tempStr := prompt("Temperature (optional, e.g. 0.7)")
	maxTokensStr := prompt("Max tokens (optional)")
	extra := prompt("Extra key=val (optional, empty to skip)")

	// Build the request map
	req := make(map[string]interface{})
	req["name"] = name
	req["type"] = providerType
	req["endpoint"] = endpoint

	if model != "" {
		req["model"] = model
	}
	if tempStr != "" {
		tempVal, err := strconv.ParseFloat(tempStr, 64)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: invalid temperature '%s': %v\n", tempStr, err)
			os.Exit(1)
		}
		req["temperature"] = tempVal
	}
	if maxTokensStr != "" {
		tokensVal, err := strconv.Atoi(maxTokensStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: invalid max_tokens '%s': %v\n", maxTokensStr, err)
			os.Exit(1)
		}
		req["max_tokens"] = tokensVal
	}
	if apiKey != "" {
		req["api_key"] = apiKey
	}
	if extra != "" {
		req["extra"] = parseExtra(extra)
	}

	// Show summary for confirmation
	fmt.Println("\n--- Provider Summary ---")
	for k, v := range req {
		fmt.Printf("  %s: %v\n", k, v)
	}
	fmt.Print("\nConfirm and create? (yes/no): ")
	scanner.Scan()
	confirm := strings.TrimSpace(strings.ToLower(scanner.Text()))

	if confirm != "yes" && confirm != "y" {
		fmt.Println("Aborted.")
		return
	}

	body, err := httpPostJSON("/api/providers", req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating provider: %v\n", err)
		os.Exit(1)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(body, &resp); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing response: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\nProvider '%s' created successfully.\n", name)
	printprovidersSummary(resp)
}

// handleProvidersUpdateInteractive runs an interactive prompt-based workflow for updating a provider.
func handleProvidersUpdateInteractive() {
	scanner := bufio.NewScanner(os.Stdin)

	promptWithCurrent := func(label string, current interface{}) string {
		if current != nil {
			fmt.Printf("%s [%v]: ", label, current)
		} else {
			fmt.Printf("%s: ", label)
		}
		scanner.Scan()
		return strings.TrimSpace(scanner.Text())
	}

	name := promptWithCurrent("Provider name", nil)
	if name == "" {
		fmt.Fprintln(os.Stderr, "Error: provider name is required")
		os.Exit(1)
	}

	// Fetch current provider data to show in prompts
	body, err := httpGet("/api/providers/" + urlPathEscape(name))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error fetching provider '%s': %v\n", name, err)
		os.Exit(1)
	}

	var current map[string]interface{}
	if err := json.Unmarshal(body, &current); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing provider data: %v\n", err)
		os.Exit(1)
	}

	validTypes := []string{"openai", "anthropic", "ollama", "gemini", "llamacpp"}
	fmt.Printf("Valid types: %s\n", strings.Join(validTypes, ", "))

	providerTypeStr := promptWithCurrent("Provider type", current["type"])
	endpoint := promptWithCurrent("Endpoint URL", current["endpoint"])

	var model string

	// Determine effective type/endpoint: use user input if provided, otherwise fall back to current values.
	effectiveType := providerTypeStr
	if effectiveType == "" {
		if t, ok := current["type"].(string); ok && t != "" {
			effectiveType = t
		}
	}
	effectiveEndpoint := endpoint
	if effectiveEndpoint == "" {
		if e, ok := current["endpoint"].(string); ok && e != "" {
			effectiveEndpoint = e
		}
	}

	if effectiveType != "" && effectiveEndpoint != "" {
		models := queryModelsForProvider(name)
		if len(models) > 0 {
			fmt.Printf("\nAvailable models for this provider (%d found):\n", len(models))
			for i, m := range models {
				fmt.Printf("  %d. %s\n", i+1, m.Id)
			}
			model = promptWithCurrent("Select a model number (or press Enter to keep current)", current["model"])
			if model != "" {
				idx, err := strconv.Atoi(model)
				if err != nil || idx < 1 || idx > len(models) {
					fmt.Fprintf(os.Stderr, "Error: invalid selection '%s'\n", model)
					os.Exit(1)
				}
				model = models[idx-1].Id
			}
		} else {
			model = promptWithCurrent("Model", current["model"])
		}
	} else {
		model = promptWithCurrent("Model", current["model"])
	}

	tempVal := current["temperature"]
	tempStr := promptWithCurrent("Temperature", fmt.Sprintf("%.2f", tempVal))
	maxTokensStr := promptWithCurrent("Max tokens", current["max_tokens"])
	apiKey := promptWithCurrent("API key", current["api_key"])
	extra := promptWithCurrent("Extra (key=val)", current["extra"])

	// Build the request map (only non-empty fields)
	req := make(map[string]interface{})

	if providerTypeStr != "" {
		req["type"] = providerTypeStr
	}
	if endpoint != "" {
		req["endpoint"] = endpoint
	}
	if model != "" {
		req["model"] = model
	}
	if tempStr != "" {
		tVal, err := strconv.ParseFloat(tempStr, 64)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: invalid temperature '%s': %v\n", tempStr, err)
			os.Exit(1)
		}
		req["temperature"] = tVal
	}
	if maxTokensStr != "" {
		tokensVal, err := strconv.Atoi(maxTokensStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: invalid max_tokens '%s': %v\n", maxTokensStr, err)
			os.Exit(1)
		}
		req["max_tokens"] = tokensVal
	}
	if apiKey != "" {
		req["api_key"] = apiKey
	}
	if extra != "" {
		req["extra"] = parseExtra(extra)
	}

	if len(req) == 0 {
		fmt.Println("No fields provided. Nothing to update.")
		return
	}

	// Show summary for confirmation
	fmt.Println("\n--- Update Summary ---")
	for k, v := range req {
		fmt.Printf("  %s: %v\n", k, v)
	}
	fmt.Print("\nConfirm and update? (yes/no): ")
	scanner.Scan()
	confirm := strings.TrimSpace(strings.ToLower(scanner.Text()))

	if confirm != "yes" && confirm != "y" {
		fmt.Println("Aborted.")
		return
	}

	url := "/api/providers/" + urlPathEscape(name)
	body, err = httpPutJSON(url, req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error updating provider '%s': %v\n", name, err)
		os.Exit(1)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(body, &resp); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing response: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\nProvider '%s' updated successfully.\n", name)
	printprovidersSummary(resp)
}

// modelEntry represents a single available model for display.
type modelEntry struct {
	Id   string
	Name string
}

// fetchAvailableModels queries the provider's API to list available models.
func fetchAvailableModels(endpoint, apiKey, providerType string) ([]modelEntry, error) {
	switch strings.ToLower(providerType) {
	case "openai", "anthropic", "gemini", "llamacpp":
		return fetchOpenAICompatibleModels(endpoint, apiKey)
	case "ollama":
		return fetchOllamaModels(endpoint)
	default:
		return nil, fmt.Errorf("model discovery not supported for type %q", providerType)
	}
}

// fetchOpenAICompatibleModels queries an OpenAI-compatible endpoint's /v1/models.
func fetchOpenAICompatibleModels(endpoint, apiKey string) ([]modelEntry, error) {
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
	_ = resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

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

	var entries []modelEntry
	for _, m := range openaiResp.Data {
		entry := modelEntry{}
		if id, ok := m["id"].(string); ok {
			entry.Id = id
		}
		if name, ok := m["name"].(string); ok {
			entry.Name = name
		} else if entry.Id != "" {
			entry.Name = entry.Id
		}
		entries = append(entries, entry)
	}

	return entries, nil
}

// fetchOllamaModels queries Ollama's /api/tags endpoint.
func fetchOllamaModels(endpoint string) ([]modelEntry, error) {
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

	var entries []modelEntry
	for _, m := range ollamaResp.Models {
		entry := modelEntry{}
		if name, ok := m["name"].(string); ok {
			entry.Id = name
			entry.Name = name
		}
		entries = append(entries, entry)
	}

	return entries, nil
}

// queryModelsForProvider queries the server's /api/providers/{name}/models endpoint
// and returns available model entries. Returns an empty slice on error (non-fatal).
func queryModelsForProvider(name string) []modelEntry {
	body, err := httpGet("/api/providers/" + urlPathEscape(name) + "/models")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not fetch available models for '%s': %v\n", name, err)
		return nil
	}

	var resp protocol.AvailableModelsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: error parsing model response: %v\n", err)
		return nil
	}

	models := []modelEntry{}
	if resp.Models != nil {
		for _, m := range *resp.Models {
			id := ""
			if m.Id != nil {
				id = *m.Id
			}
			nameVal := ""
			if m.Name != nil {
				nameVal = *m.Name
			}
			models = append(models, modelEntry{Id: id, Name: nameVal})
		}
	}

	return models
}

// printprovidersSummary prints a provider summary from a response map.
func printprovidersSummary(p map[string]interface{}) {
	name, _ := p["name"].(string)
	if name == "" {
		name = "(unknown)"
	}
	fmt.Printf("  [%s]\n", name)
	fmt.Printf("    type:       %v\n", p["type"])
	fmt.Printf("    endpoint:   %v\n", p["endpoint"])
	fmt.Printf("    model:      %v\n", p["model"])
	if temp, ok := p["temperature"].(float64); ok {
		fmt.Printf("    temperature: %.2f\n", temp)
	}
	if mt, ok := p["max_tokens"].(float64); ok {
		fmt.Printf("    max_tokens: %d\n", int(mt))
	}
	if masked, ok := p["api_key_masked"].(bool); ok && masked {
		fmt.Printf("    api_key:    (masked)\n")
	}
}

// urlPathEscape safely escapes a name for URL path segments.
func urlPathEscape(s string) string {
	return strings.ReplaceAll(s, "/", "%2F")
}
