package main

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

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
// If no flags are provided, falls back to the interactive workflow.
func handleProvidersAdd(ctx context.Context, cmd *cli.Command) error {
	name := cmd.Args().First()
	if name == "" {
		return fmt.Errorf("provider name is required")
	}

	// Detect if NO values were provided — fall back to interactive mode.
	noFlags := cmd.String("type") == "" &&
		cmd.String("endpoint") == "" &&
		cmd.String("model") == "" &&
		!cmd.IsSet("temp") &&
		!cmd.IsSet("max-tokens") &&
		cmd.String("api-key") == "" &&
		cmd.Args().Get(1) == ""

	if noFlags {
		return handleProvidersAddInteractive(ctx)
	}

	req := protocol.ProviderCreateRequest{
		Name:        name,
		Type:        protocol.ProviderCreateRequestType(cmd.String("type")),
		Endpoint:    cmd.String("endpoint"),
		Model:       strPtr(cmd.String("model")),
		Temperature: float32Ptr(cmd.Float64("temp")),
		MaxTokens:   intPtr(cmd.Int("max-tokens")),
		ApiKey:      strPtr(cmd.String("api-key")),
	}

	if cmd.IsSet("extra") {
		extraMap := parseExtra(cmd.Args().Get(1))
		req.Extra = &extraMap
	}

	client, err := getProtocolClient()
	if err != nil {
		return fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := client.CreateProvider(ctx, req)
	if err != nil {
		return fmt.Errorf("error creating provider: %w", err)
	}

	parsed, err := protocol.ParseCreateProviderResponse(resp)
	if err != nil {
		return fmt.Errorf("error parsing response: %w", err)
	}

	fmt.Printf("Provider '%s' created successfully.\n", name)
	printprovidersSummaryTyped(parsed.JSON201)
	return nil
}

// handleProvidersList lists all configured providers.
func handleProvidersList(ctx context.Context, cmd *cli.Command) error {
	client, err := getProtocolClient()
	if err != nil {
		return fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := client.ListProviders(ctx)
	if err != nil {
		return fmt.Errorf("error fetching providers: %w", err)
	}

	parsed, err := protocol.ParseListProvidersResponse(resp)
	if err != nil {
		return fmt.Errorf("error parsing response: %w", err)
	}

	if parsed.JSON200 == nil || len(*parsed.JSON200) == 0 {
		fmt.Println("No providers configured.")
		return nil
	}

	providers := *parsed.JSON200
	fmt.Printf("\nConfigured LLM Providers (%d):\n", len(providers))
	fmt.Println(strings.Repeat("-", 70))
	for i, p := range providers {
		printprovidersSummaryTyped(&p)
		if i < len(providers)-1 {
			fmt.Println(strings.Repeat("-", 70))
		}
	}
	fmt.Println()
	return nil
}

// handleProvidersGet shows details for a specific provider.
func handleProvidersGet(ctx context.Context, cmd *cli.Command) error {
	name := cmd.Args().First()
	if name == "" {
		return fmt.Errorf("provider name is required")
	}

	client, err := getProtocolClient()
	if err != nil {
		return fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := client.GetProvider(ctx, urlPathEscape(name))
	if err != nil {
		return fmt.Errorf("error fetching provider '%s': %w", name, err)
	}

	parsed, err := protocol.ParseGetProviderResponse(resp)
	if err != nil {
		return fmt.Errorf("error parsing response: %w", err)
	}

	if parsed.JSON200 == nil {
		return fmt.Errorf("provider '%s' not found", name)
	}

	fmt.Printf("Provider '%s':\n", name)
	printprovidersSummaryTyped(parsed.JSON200)
	return nil
}

// handleProvidersUpdate updates an existing provider's fields.
// If no flags are provided, falls back to the interactive workflow.
func handleProvidersUpdate(ctx context.Context, cmd *cli.Command) error {
	name := cmd.Args().First()
	if name == "" {
		return fmt.Errorf("provider name is required")
	}

	// Detect if NO values were provided — fall back to interactive mode.
	noFlags := cmd.String("type") == "" &&
		cmd.String("endpoint") == "" &&
		cmd.String("model") == "" &&
		!cmd.IsSet("temp") &&
		!cmd.IsSet("max-tokens") &&
		cmd.String("api-key") == ""

	if noFlags {
		return handleProvidersUpdateInteractive(ctx, name)
	}

	req := protocol.ProviderUpdateRequest{
		Endpoint:    strPtr(cmd.String("endpoint")),
		Model:       strPtr(cmd.String("model")),
		Temperature: float32Ptr(cmd.Float64("temp")),
		MaxTokens:   intPtr(cmd.Int("max-tokens")),
		ApiKey:      strPtr(cmd.String("api-key")),
	}
	if t := cmd.String("type"); t != "" {
		v := protocol.ProviderUpdateRequestType(t)
		req.Type = &v
	}

	client, err := getProtocolClient()
	if err != nil {
		return fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := client.UpdateProvider(ctx, urlPathEscape(name), req)
	if err != nil {
		return fmt.Errorf("error updating provider '%s': %w", name, err)
	}

	parsed, err := protocol.ParseUpdateProviderResponse(resp)
	if err != nil {
		return fmt.Errorf("error parsing response: %w", err)
	}

	fmt.Printf("Provider '%s' updated successfully.\n", name)
	printprovidersSummaryTyped(parsed.JSON200)
	return nil
}

// handleProvidersDelete deletes a provider via DELETE /api/providers/{name}.
func handleProvidersDelete(ctx context.Context, cmd *cli.Command) error {
	name := cmd.Args().First()
	if name == "" {
		return fmt.Errorf("provider name is required")
	}

	client, err := getProtocolClient()
	if err != nil {
		return fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := client.DeleteProvider(ctx, urlPathEscape(name))
	if err != nil {
		return fmt.Errorf("error deleting provider '%s': %w", name, err)
	}

	_, _ = protocol.ParseDeleteProviderResponse(resp)
	fmt.Printf("Provider '%s' removed.\n", name)
	return nil
}

// handleProvidersSetDefault sets the default provider via config update.
func handleProvidersSetDefault(ctx context.Context, cmd *cli.Command) error {
	name := cmd.Args().First()
	if name == "" {
		return fmt.Errorf("provider name is required\nUsage: norman providers set-default <name>")
	}

	client, err := getProtocolClient()
	if err != nil {
		return fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := client.PutConfig(ctx, map[string]interface{}{
		"default_provider": name,
	})
	if err != nil {
		return fmt.Errorf("error setting default provider '%s': %w", name, err)
	}

	_, _ = protocol.ParsePutConfigResponse(resp)
	fmt.Printf("Default provider set to '%s'.\n", name)
	return nil
}

// handleProvidersListModels lists available models for a provider.
func handleProvidersListModels(ctx context.Context, cmd *cli.Command) error {
	name := cmd.Args().First()
	if name == "" {
		return fmt.Errorf("provider name is required\nUsage: norman providers list-models <name>")
	}

	client, err := getProtocolClient()
	if err != nil {
		return fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := client.ListProviderModels(ctx, urlPathEscape(name))
	if err != nil {
		return fmt.Errorf("error fetching models for provider '%s': %w", name, err)
	}

	parsed, err := protocol.ParseListProviderModelsResponse(resp)
	if err != nil {
		return fmt.Errorf("error parsing model response: %w", err)
	}

	if parsed.JSON200 == nil || parsed.JSON200.Models == nil || len(*parsed.JSON200.Models) == 0 {
		fmt.Printf("\nNo models found for provider '%s'.\n", name)
		return nil
	}

	models := *parsed.JSON200.Models
	fmt.Printf("\nAvailable models for provider '%s' (%d found):\n\n", name, len(models))
	fmt.Printf("%-4s %-30s %s\n", "#", "ID", "Name")
	fmt.Println(strings.Repeat("-", 70))
	for i, m := range models {
		nameField := ""
		if m.Name != nil {
			nameField = *m.Name
		}
		if nameField == "" {
			nameField = "-"
		}
		fmt.Printf("%-4d %-30s %s\n", i+1, *m.Id, nameField)
	}
	fmt.Println()
	return nil
}

// handleProvidersAddInteractive runs an interactive prompt-based workflow for adding a provider.
func handleProvidersAddInteractive(ctx context.Context) error {
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

	// Query available models via the server API.
	var models []protocol.AvailableModelEntry
	var resp *http.Response
	client, err := getProtocolClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not create API client: %v\n", err)
	} else {
		queryReq := protocol.QueryProviderModelsRequest{
			Type:     providerType,
			Endpoint: endpoint,
			ApiKey:   strPtr(apiKey),
		}
		resp, err = client.QueryProviderModels(ctx, queryReq)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not fetch available models: %v\n", err)
			fmt.Println("You can enter a model name manually below.")
		} else {
			parsed, err := protocol.ParseQueryProviderModelsResponse(resp)
			if err == nil && parsed.JSON200 != nil && parsed.JSON200.Models != nil {
				models = *parsed.JSON200.Models
			}
		}
	}

	var model string
	if len(models) > 0 {
		fmt.Printf("\nAvailable models for this provider (%d found):\n", len(models))
		for i, m := range models {
			fmt.Printf("  %d. %s\n", i+1, strPtrVal(m.Id))
		}
		model = prompt("Select a model number (or press Enter to skip)")
		if model != "" {
			idx, err := strconv.Atoi(model)
			if err != nil || idx < 1 || idx > len(models) {
				fmt.Fprintf(os.Stderr, "Error: invalid selection '%s'\n", model)
				os.Exit(1)
			}
			model = strPtrVal(models[idx-1].Id)
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
		return nil
	}

	client, err = getProtocolClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to create API client: %v\n", err)
		os.Exit(1)
	}

	reqTyped := protocol.ProviderCreateRequest{
		Name:        name,
		Type:        protocol.ProviderCreateRequestType(providerType),
		Endpoint:    endpoint,
		Model:       strPtr(model),
		Temperature: parseFloat(tempStr),
		MaxTokens:   parseInt(maxTokensStr),
		ApiKey:      strPtr(apiKey),
	}
	if extra != "" {
		extraMap := parseExtra(extra)
		reqTyped.Extra = &extraMap
	}

	resp, err = client.CreateProvider(ctx, reqTyped)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating provider: %v\n", err)
		os.Exit(1)
	}

	parsed, err := protocol.ParseCreateProviderResponse(resp)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing response: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\nProvider '%s' created successfully.\n", name)
	printprovidersSummaryTyped(parsed.JSON201)
	return nil
}

// handleProvidersUpdateInteractive runs an interactive prompt-based workflow for updating a provider.
func handleProvidersUpdateInteractive(ctx context.Context, name string) error {
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

	prompt := func(label string) string {
		fmt.Printf("%s: ", label)
		scanner.Scan()
		return strings.TrimSpace(scanner.Text())
	}

	client, err := getProtocolClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to create API client: %v\n", err)
		os.Exit(1)
	}

	resp, err := client.GetProvider(ctx, urlPathEscape(name))
	if err != nil {
		return fmt.Errorf("error fetching provider '%s': %w", name, err)
	}

	parsed, err := protocol.ParseGetProviderResponse(resp)
	if err != nil {
		return fmt.Errorf("error parsing provider data: %w", err)
	}

	if parsed.JSON200 == nil {
		return fmt.Errorf("provider '%s' not found", name)
	}

	current := *parsed.JSON200

	validTypes := []string{"openai", "anthropic", "ollama", "gemini", "llamacpp"}
	fmt.Printf("Valid types: %s\n", strings.Join(validTypes, ", "))

	providerTypeStr := promptWithCurrent("Provider type", strPtrVal(current.Type))
	endpoint := promptWithCurrent("Endpoint URL", strPtrVal(current.Endpoint))

	var model string

	// Determine effective type/endpoint: use user input if provided, otherwise fall back to current values.
	effectiveType := providerTypeStr
	if effectiveType == "" {
		if t := current.Type; t != nil && *t != "" {
			effectiveType = string(*t)
		}
	}
	effectiveEndpoint := endpoint
	if effectiveEndpoint == "" {
		if e := current.Endpoint; e != nil && *e != "" {
			effectiveEndpoint = *e
		}
	}

	if effectiveType != "" && effectiveEndpoint != "" {
		var models []protocol.AvailableModelEntry
		client, err := getProtocolClient()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to create API client: %v\n", err)
		} else {
			resp, err := client.ListProviderModels(ctx, urlPathEscape(name))
			if err == nil {
				parsed, err := protocol.ParseListProviderModelsResponse(resp)
				if err == nil && parsed.JSON200 != nil && parsed.JSON200.Models != nil {
					models = *parsed.JSON200.Models
				}
			}
		}
		if len(models) > 0 {
			fmt.Printf("\nAvailable models for this provider (%d found):\n", len(models))
			for i, m := range models {
				fmt.Printf("  %d. %s\n", i+1, strPtrVal(m.Id))
			}
			model = promptWithCurrent("Select a model number (or press Enter to keep current)", strPtrVal(current.Model))
			if model != "" {
				idx, err := strconv.Atoi(model)
				if err != nil || idx < 1 || idx > len(models) {
					fmt.Fprintf(os.Stderr, "Error: invalid selection '%s'\n", model)
					os.Exit(1)
				}
				model = strPtrVal(models[idx-1].Id)
			}
		} else {
			model = promptWithCurrent("Model", strPtrVal(current.Model))
		}
	} else {
		model = promptWithCurrent("Model", strPtrVal(current.Model))
	}

	tempVal := current.Temperature
	var tempStr string
	if tempVal != nil {
		tempStr = promptWithCurrent("Temperature", fmt.Sprintf("%.2f", float64(*tempVal)))
	} else {
		tempStr = prompt("Temperature")
	}
	var maxTokensStr string
	if current.MaxTokens != nil {
		maxTokensStr = promptWithCurrent("Max tokens", fmt.Sprintf("%d", *current.MaxTokens))
	} else {
		maxTokensStr = prompt("Max tokens")
	}
	apiKeyMasked := "(masked)"
	if current.ApiKeyMasked != nil && !*current.ApiKeyMasked {
		apiKeyMasked = "(unmasked - not shown)"
	}
	apiKey := promptWithCurrent("API key", apiKeyMasked)
	var extra string
	if current.Extra != nil {
		extra = promptWithCurrent("Extra (key=val)", fmt.Sprintf("%v", *current.Extra))
	} else {
		extra = prompt("Extra (key=val)")
	}

	// Build the request map (only non-empty fields)
	req := protocol.ProviderUpdateRequest{
		Endpoint:    strPtr(endpoint),
		Model:       strPtr(model),
		Temperature: parseFloat(tempStr),
		MaxTokens:   parseInt(maxTokensStr),
		ApiKey:      strPtr(apiKey),
	}
	if providerTypeStr != "" {
		v := protocol.ProviderUpdateRequestType(providerTypeStr)
		req.Type = &v
	}
	if extra != "" {
		extraMap := parseExtra(extra)
		req.Extra = &extraMap
	}

	if providerTypeStr == "" && endpoint == "" && model == "" && tempStr == "" && maxTokensStr == "" && apiKey == "" && extra == "" {
		fmt.Println("No fields provided. Nothing to update.")
		return nil
	}

	// Show summary for confirmation
	fmt.Println("\n--- Update Summary ---")
	for k, v := range map[string]interface{}{
		"type":        providerTypeStr,
		"endpoint":    endpoint,
		"model":       model,
		"temperature": tempStr,
		"max_tokens":  maxTokensStr,
		"api_key":     apiKey,
		"extra":       extra,
	} {
		fmt.Printf("  %s: %v\n", k, v)
	}
	fmt.Print("\nConfirm and update? (yes/no): ")
	scanner.Scan()
	confirm := strings.TrimSpace(strings.ToLower(scanner.Text()))

	if confirm != "yes" && confirm != "y" {
		fmt.Println("Aborted.")
		return nil
	}

	resp, err = client.UpdateProvider(ctx, urlPathEscape(name), req)
	if err != nil {
		return fmt.Errorf("error updating provider '%s': %w", name, err)
	}

	updateParsed, err := protocol.ParseUpdateProviderResponse(resp)
	if err != nil {
		return fmt.Errorf("error parsing response: %w", err)
	}

	fmt.Printf("\nProvider '%s' updated successfully.\n", name)
	printprovidersSummaryTyped(updateParsed.JSON200)
	return nil
}

// printprovidersSummaryTyped prints a provider summary from a typed response.
func printprovidersSummaryTyped(p *protocol.ProviderResponse) {
	if p == nil {
		fmt.Println("  (unknown)")
		return
	}
	name := "(unknown)"
	if p.Name != nil {
		name = *p.Name
	}
	fmt.Printf("  [%s]\n", name)
	fmt.Printf("    type:       %v\n", strPtrVal(p.Type))
	fmt.Printf("    endpoint:   %v\n", strVal(p.Endpoint))
	fmt.Printf("    model:      %v\n", strVal(p.Model))
	if p.Temperature != nil {
		fmt.Printf("    temperature: %.2f\n", float64(*p.Temperature))
	}
	if p.MaxTokens != nil {
		fmt.Printf("    max_tokens: %d\n", *p.MaxTokens)
	}
	if p.ApiKeyMasked != nil && *p.ApiKeyMasked {
		fmt.Printf("    api_key:    (masked)\n")
	}
}
