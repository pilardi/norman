package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/urfave/cli/v3"
)

// reservedExtraKeys are struct field names that must not appear in the extra map.
var _ = reservedExtraKeys
var reservedExtraKeys = map[string]struct{}{
	"name":           {},
	"type":           {},
	"endpoint":       {},
	"model":          {},
	"temperature":    {},
	"max_tokens":     {},
	"api_key_masked": {},
}

// parseExtra parses "key=val,key2=val2" into a map[string]interface{}.
var _ = parseExtra

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
				if _, exists := reservedExtraKeys[key]; !exists {
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

	fmt.Printf("Configured providers (%d):\n\n", len(providers))
	for _, p := range providers {
		name, _ := p["name"].(string)
		typ, _ := p["type"].(string)
		endpoint, _ := p["endpoint"].(string)
		model, _ := p["model"].(string)
		temp, _ := p["temperature"].(float64)
		maxTokens, _ := p["max_tokens"].(float64)

		fmt.Printf("  %s [%s]\n", name, typ)
		fmt.Printf("    endpoint:  %s\n", endpoint)
		fmt.Printf("    model:     %s\n", model)
		if temp > 0 {
			fmt.Printf("    temp:      %.2f\n", temp)
		}
		if maxTokens > 0 {
			fmt.Printf("    max_tokens: %d\n", int(maxTokens))
		}
		fmt.Println()
	}
	return nil
}

// handleProvidersGet shows details for a specific provider.
func handleProvidersGet(name string) error {
	body, err := httpGet("/api/providers/" + name)
	if err != nil {
		return fmt.Errorf("error fetching provider '%s': %v", name, err)
	}

	var provider map[string]interface{}
	if err := json.Unmarshal(body, &provider); err != nil {
		return fmt.Errorf("error parsing provider response: %v", err)
	}

	printprovidersSummary(provider)
	return nil
}

// handleProvidersUpdate updates an existing provider's fields.
func handleProvidersUpdate(ctx context.Context, cmd *cli.Command) error {
	name := cmd.Args().First()
	if name == "" {
		return fmt.Errorf("provider name is required")
	}

	// If no update flags are provided, fall back to interactive mode.
	if !cmd.IsSet("type") && !cmd.IsSet("endpoint") && !cmd.IsSet("model") &&
		!cmd.IsSet("temp") && !cmd.IsSet("max-tokens") && !cmd.IsSet("api-key") {
		handleProvidersUpdateInteractive(name)
		return nil
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

	body, err := httpPutJSON("/api/providers/"+name, req)
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

// handleProvidersDelete removes a provider.
func handleProvidersDelete(name string) error {
	if err := httpDelete("/api/providers/" + name); err != nil {
		return fmt.Errorf("error deleting provider '%s': %v", name, err)
	}
	fmt.Printf("Provider '%s' deleted successfully.\n", name)
	return nil
}

// handleProvidersSetDefault sets the default provider.
func handleProvidersSetDefault(name string) error {
	req := map[string]interface{}{"default_provider": name}
	body, err := httpPostJSON("/api/providers/set-default", req)
	if err != nil {
		return fmt.Errorf("error setting default provider: %v", err)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(body, &resp); err != nil {
		return fmt.Errorf("error parsing response: %v", err)
	}

	fmt.Printf("Default provider set to '%s'.\n", name)
	printprovidersSummary(resp)
	return nil
}

// handleProvidersListModels lists available models for a provider.
func handleProvidersListModels(name string) error {
	body, err := httpGet("/api/providers/" + name + "/models")
	if err != nil {
		return fmt.Errorf("error listing models for '%s': %v", name, err)
	}

	var models []map[string]interface{}
	if err := json.Unmarshal(body, &models); err != nil {
		return fmt.Errorf("error parsing models response: %v", err)
	}

	fmt.Printf("Available models for provider '%s':\n\n", name)
	for _, m := range models {
		modelName, _ := m["name"].(string)
		desc, _ := m["description"].(string)
		if desc != "" {
			fmt.Printf("  %-30s %s\n", modelName, desc)
		} else {
			fmt.Printf("  %s\n", modelName)
		}
	}
	return nil
}

// printprovidersSummary prints a provider summary from a response map.
func printprovidersSummary(resp map[string]interface{}) {
	name, _ := resp["name"].(string)
	typ, _ := resp["type"].(string)
	endpoint, _ := resp["endpoint"].(string)
	model, _ := resp["model"].(string)
	temp, _ := resp["temperature"].(float64)
	maxTokens, _ := resp["max_tokens"].(float64)

	fmt.Printf("  name:       %s\n", name)
	fmt.Printf("  type:       %s\n", typ)
	fmt.Printf("  endpoint:   %s\n", endpoint)
	fmt.Printf("  model:      %s\n", model)
	if temp > 0 {
		fmt.Printf("  temperature: %.2f\n", temp)
	}
	if maxTokens > 0 {
		fmt.Printf("  max_tokens:  %d\n", int(maxTokens))
	}
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
	_ = apiKey // stored in provider config

	modelName := prompt("Model name (optional)")
	_ = modelName

	tempStr := prompt("Temperature (0.0–2.0, optional)")
	tempVal, _ := strconv.ParseFloat(tempStr, 64)
	_ = tempStr

	maxTokensStr := prompt("Max tokens (optional)")
	maxTokens, _ := strconv.Atoi(maxTokensStr)
	_ = maxTokensStr

	req := map[string]interface{}{
		"name":        name,
		"type":        providerType,
		"endpoint":    endpoint,
		"model":       modelName,
		"temperature": tempVal,
		"max_tokens":  maxTokens,
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

	fmt.Printf("Provider '%s' created successfully.\n", name)
	printprovidersSummary(resp)
}

// handleProvidersUpdateInteractive runs an interactive prompt-based workflow for updating a provider.
func handleProvidersUpdateInteractive(name string) {
	scanner := bufio.NewScanner(os.Stdin)

	// Fetch current provider data to populate defaults and show current values.
	body, err := httpGet("/api/providers/" + name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error fetching provider '%s': %v\n", name, err)
		os.Exit(1)
	}

	var provider map[string]interface{}
	if err := json.Unmarshal(body, &provider); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing provider data: %v\n", err)
		os.Exit(1)
	}

	req := map[string]interface{}{"name": name}
	// Copy current values into req for display purposes.
	for k, v := range provider {
		if k != "api_key_masked" {
			req[k] = v
		}
	}

	fmt.Printf("\nProvider '%s' configuration:\n", name)
	printprovidersSummary(provider)

	fields := []struct {
		key   string
		label string
	}{
		{"type", "type"},
		{"endpoint", "endpoint"},
		{"model", "model"},
		{"temperature", "temp"},
		{"max_tokens", "max_tokens"},
		{"api_key", "api-key"},
	}

	fmt.Printf("\n  Select field number (1-6) and press Enter to update, or Enter alone to finish:\n\n")

	for i, f := range fields {
		currentVal := req[f.key]
		currentStr := fmt.Sprintf("%v", currentVal)
		if currentStr == "<nil>" {
			currentStr = "(empty)"
		}
		fmt.Printf("  %d. %-12s [%s]\n", i+1, f.label, currentStr)
	}

	for {
		fmt.Printf("\n  Field number (1-6) or 'done': ")
		line := scanner.Scan()
		if !line {
			break
		}
		input := strings.TrimSpace(scanner.Text())

		if input == "" || strings.ToLower(input) == "done" {
			break
		}

		// Parse field number
		idx, err := strconv.Atoi(input)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  Invalid field number: %s\n", input)
			continue
		}
		if idx < 1 || idx > len(fields) {
			fmt.Fprintf(os.Stderr, "  Valid range: 1-%d\n", len(fields))
			continue
		}

		f := fields[idx-1]
		currentVal := req[f.key]
		currentStr := fmt.Sprintf("%v", currentVal)
		if currentStr == "<nil>" {
			currentStr = "(empty)"
		}

		fmt.Printf("  %-12s [%s] -> ", f.label, currentStr)
		line = scanner.Scan()
		if !line {
			break
		}
		value := strings.TrimSpace(scanner.Text())

		if value == "" || strings.ToLower(value) == "done" {
			break
		}

		switch f.key {
		case "type":
			req["type"] = value

		case "endpoint":
			req["endpoint"] = value

		case "model":
			// If input looks like a number, try to resolve from model list.
			if idx2, err := strconv.Atoi(value); err == nil {
				modelBody, err2 := httpGet("/api/providers/" + name + "/models")
				if err2 == nil {
					var resp map[string]interface{}
					if json.Unmarshal(modelBody, &resp) == nil {
						if modelsArr, ok := resp["models"].([]interface{}); ok {
							if idx2 >= 1 && idx2 <= len(modelsArr) {
								if mm, ok := modelsArr[idx2-1].(map[string]interface{}); ok {
									modelID, _ := mm["id"].(string)
									req["model"] = modelID
									continue
								}
							}
						}
					}
				}
			}
			req["model"] = value

		case "temperature":
			if v, err := strconv.ParseFloat(value, 64); err == nil {
				req["temperature"] = v
			} else {
				fmt.Fprintf(os.Stderr, "  Invalid number: %s\n", value)
			}

		case "max_tokens":
			if v, err := strconv.Atoi(value); err == nil {
				req["max_tokens"] = v
			} else {
				fmt.Fprintf(os.Stderr, "  Invalid number: %s\n", value)
			}

		case "api_key":
			req["api_key"] = value
		}
	}

	body, err = httpPutJSON("/api/providers/"+name, req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error updating provider '%s': %v\n", name, err)
		os.Exit(1)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(body, &resp); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing response: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Provider '%s' updated successfully.\n", name)
	printprovidersSummary(resp)
}
