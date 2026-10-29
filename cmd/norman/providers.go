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

	body, err := httpPostJSON("/api/providers/"+name+"/update", req)
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

// printprovidersHelp displays help for the `norman provider` subcommands.
var _ = printprovidersHelp

func printprovidersHelp() {
	fmt.Println(`provider — Manage LLM providers (add, list, get, update, delete)

Usage: norman providers <subcommand> [args...]

Subcommands:
  add [name] [--type <type>] [--endpoint <url>] [--model <model>] [--temp <float>] [--max-tokens <int>] [--api-key <key>] [--extra key=val]
      Add a new LLM provider. Required flags: --type, --endpoint.
      Type must be one of: openai, anthropic, ollama, gemini, llamacpp.

  list (ls)
      List all configured providers with their settings.

  get <name>
      Show full details for a specific provider by name.

  update <name> [--type <type>] [--endpoint <url>] [--model <model>] [--temp <float>] [--max-tokens <int>] [--api-key <key>] [--extra key=val]
      Update an existing provider's fields (partial update).

  delete <name> (del)
      Remove a provider. Fails if it's the last remaining one.

  set-default <name> (sd)
      Set the default provider for sessions and CLI operations.

  list-models <name> (lm)
      List available models for a configured provider by querying its API endpoint.

Examples:
  norman providers add local --type ollama --endpoint http://localhost:11434 --model llama3
  norman providers add openai-default --type openai --endpoint https://api.openai.com/v1 --model gpt-4o --temp 0.7 --max-tokens 8192
  norman providers list
  norman providers get local
  norman providers update local --temp 0.5
  norman providers delete old-provider
  norman providers set-default local
  norman providers list-models local`)
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
var _ = handleProvidersUpdateInteractive

func handleProvidersUpdateInteractive() {
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

	req := map[string]interface{}{"name": name}

	for {
		fmt.Printf("\nUpdate field (type/endpoint/model/temp/max_tokens/api-key) or 'done': ")
		scanner.Scan()
		field := strings.TrimSpace(scanner.Text())
		if field == "done" || field == "" {
			break
		}

		fmt.Printf("  New value: ")
		scanner.Scan()
		value := strings.TrimSpace(scanner.Text())

		switch field {
		case "type":
			req["type"] = value
		case "endpoint":
			req["endpoint"] = value
		case "model":
			req["model"] = value
		case "temp":
			if v, err := strconv.ParseFloat(value, 64); err == nil {
				req["temperature"] = v
			}
		case "max_tokens":
			if v, err := strconv.Atoi(value); err == nil {
				req["max_tokens"] = v
			}
		case "api-key":
			req["api_key"] = value
		default:
			fmt.Fprintf(os.Stderr, "Unknown field: %s\n", field)
		}
	}

	body, err := httpPostJSON("/api/providers/"+name+"/update", req)
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
