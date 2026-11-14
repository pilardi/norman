package main

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/norman/norman/pkg/client"
	"github.com/norman/norman/pkg/config"
	"github.com/norman/norman/pkg/protocol"
	"github.com/urfave/cli/v3"
)

// validProviderTypes is the single source of truth for provider type names.
// Derived from protocol.ProviderConfigType constants so adding a new type
// to the spec will cause compile-time failures in any stale literal reference.
var validProviderTypes = []string{
	string(protocol.ProviderConfigTypeOpenai),
	string(protocol.ProviderConfigTypeAnthropic),
	string(protocol.ProviderConfigTypeOllama),
	string(protocol.ProviderConfigTypeGemini),
	string(protocol.ProviderConfigTypeLlamacpp),
}

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
		return handleProvidersAddInteractive(ctx, name)
	}

	req := protocol.ProviderCreateRequest{
		Name:        name,
		Type:        protocol.ProviderCreateRequestType(cmd.String("type")),
		Endpoint:    cmd.String("endpoint"),
		Model:       client.StrPtr(cmd.String("model")),
		Temperature: client.Float32Ptr(cmd.Float64("temp")),
		MaxTokens:   client.IntPtr(cmd.Int("max-tokens")),
		ApiKey:      client.StrPtr(cmd.String("api-key")),
	}

	if cmd.IsSet("extra") {
		extraMap := parseExtra(cmd.Args().Get(1))
		req.Extra = &extraMap
	}

	// Pre-create verification: test the proposed config before persisting it.
	provCfg := &config.ProviderConfig{
		Name:        name,
		Type:        cmd.String("type"),
		Endpoint:    cmd.String("endpoint"),
		Model:       cmd.String("model"),
		Temperature: cmd.Float64("temp"),
		MaxTokens:   cmd.Int("max-tokens"),
		ApiKey:      cmd.String("api-key"),
	}
	if err := verifyConfig(ctx, provCfg); err != nil {
		return fmt.Errorf("provider verification failed — %w", err)
	}

	c, err := client.GetProtocolClient()
	if err != nil {
		return fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := c.CreateProvider(ctx, req)
	if err != nil {
		return fmt.Errorf("error creating provider: %w", err)
	}

	parsed, err := protocol.ParseCreateProviderResponse(resp)
	if err != nil {
		return fmt.Errorf("error parsing response: %w", err)
	}

	if parsed.JSON201 == nil {
		// Non-201 response (e.g. 400 validation error). Show the server's error message.
		errMsg := string(parsed.Body)
		if errMsg == "" {
			return fmt.Errorf("provider creation failed with HTTP %d", resp.StatusCode)
		}
		return fmt.Errorf("provider creation failed — %s", strings.TrimSpace(errMsg))
	}

	fmt.Printf("Provider '%s' created successfully.\n", name)
	printprovidersSummaryTyped(parsed.JSON201)
	return nil
}

// handleProvidersList lists all configured providers.
func handleProvidersList(ctx context.Context, cmd *cli.Command) error {
	c, err := client.GetProtocolClient()
	if err != nil {
		return fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := c.ListProviders(ctx)
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

	c, err := client.GetProtocolClient()
	if err != nil {
		return fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := c.GetProvider(ctx, client.UrlPathEscape(name))
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
		Endpoint:    client.StrPtr(cmd.String("endpoint")),
		Model:       client.StrPtr(cmd.String("model")),
		Temperature: client.Float32Ptr(cmd.Float64("temp")),
		MaxTokens:   client.IntPtr(cmd.Int("max-tokens")),
		ApiKey:      client.StrPtr(cmd.String("api-key")),
	}
	if t := cmd.String("type"); t != "" {
		v := protocol.ProviderUpdateRequestType(t)
		req.Type = &v
	}

	c, err := client.GetProtocolClient()
	if err != nil {
		return fmt.Errorf("failed to create API client: %w", err)
	}

	// Pre-update verification: merge old config with new values and test.
	existingResp, err := c.GetProvider(ctx, client.UrlPathEscape(name))
	if err != nil {
		return fmt.Errorf("error fetching provider '%s': %w", name, err)
	}
	existingParsed, err := protocol.ParseGetProviderResponse(existingResp)
	if err != nil {
		return fmt.Errorf("error parsing provider response: %w", err)
	}
	if existingParsed.JSON200 == nil {
		return fmt.Errorf("provider '%s' not found", name)
	}

	old := existingParsed.JSON200
	var temp float64
	if old.Temperature != nil {
		temp = float64(*old.Temperature)
	}
	newCfg := &config.ProviderConfig{
		Name:        strVal(old.Name),
		Type:        client.StrPtrVal(old.Type),
		Endpoint:    strVal(old.Endpoint),
		Model:       client.StrPtrVal(old.Model),
		Temperature: temp,
		MaxTokens:   intPtrVal(old.MaxTokens),
	}

	if v := cmd.String("type"); v != "" {
		newCfg.Type = v
	}
	if v := cmd.String("endpoint"); v != "" {
		newCfg.Endpoint = v
	}
	if v := cmd.String("model"); v != "" {
		newCfg.Model = v
	}
	if cmd.IsSet("temp") {
		newCfg.Temperature = cmd.Float64("temp")
	}
	if cmd.IsSet("max-tokens") {
		newCfg.MaxTokens = cmd.Int("max-tokens")
	}
	if cmd.IsSet("api-key") {
		newCfg.ApiKey = cmd.String("api-key")
	}

	if err := verifyConfig(ctx, newCfg); err != nil {
		return fmt.Errorf("provider verification failed — %w", err)
	}

	resp, err := c.UpdateProvider(ctx, client.UrlPathEscape(name), req)
	if err != nil {
		return fmt.Errorf("error updating provider '%s': %w", name, err)
	}

	parsed, err := protocol.ParseUpdateProviderResponse(resp)
	if err != nil {
		return fmt.Errorf("error parsing response: %w", err)
	}

	if parsed.JSON200 == nil {
		errMsg := string(parsed.Body)
		if errMsg == "" {
			return fmt.Errorf("provider update failed with HTTP %d", resp.StatusCode)
		}
		return fmt.Errorf("provider update failed — %s", strings.TrimSpace(errMsg))
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

	c, err := client.GetProtocolClient()
	if err != nil {
		return fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := c.DeleteProvider(ctx, client.UrlPathEscape(name))
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

	c, err := client.GetProtocolClient()
	if err != nil {
		return fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := c.PutConfig(ctx, map[string]interface{}{
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

	c, err := client.GetProtocolClient()
	if err != nil {
		return fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := c.ListProviderModels(ctx, client.UrlPathEscape(name))
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
// The `name` parameter is the pre-parsed CLI positional argument; if empty, prompt interactively.
func handleProvidersAddInteractive(ctx context.Context, name string) error {
	scanner := bufio.NewScanner(os.Stdin)

	prompt := func(label string) string {
		fmt.Printf("%s: ", label)
		scanner.Scan()
		return strings.TrimSpace(scanner.Text())
	}

	if name == "" {
		name = prompt("Provider name")
		if name == "" {
			fmt.Fprintln(os.Stderr, "Error: provider name is required")
			os.Exit(1)
		}
	} else {
		fmt.Printf("Using pre-supplied provider name: %s\n", name)
	}

	fmt.Printf("Valid types: %s\n", strings.Join(validProviderTypes, ", "))
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
	c, err := client.GetProtocolClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not create API client: %v\n", err)
	} else {
		queryReq := protocol.QueryProviderModelsRequest{
			Type:     providerType,
			Endpoint: endpoint,
			ApiKey:   client.StrPtr(apiKey),
		}
		resp, err = c.QueryProviderModels(ctx, queryReq)
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
			fmt.Printf("  %d. %s\n", i+1, client.StrPtrVal(m.Id))
		}
		model = prompt("Select a model number (or press Enter to skip)")
		if model != "" {
			idx, err := strconv.Atoi(model)
			if err != nil || idx < 1 || idx > len(models) {
				fmt.Fprintf(os.Stderr, "Error: invalid selection '%s'\n", model)
				os.Exit(1)
			}
			model = client.StrPtrVal(models[idx-1].Id)
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

	_, err = client.GetProtocolClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to create API client: %v\n", err)
		os.Exit(1)
	}

	reqTyped := protocol.ProviderCreateRequest{
		Name:        name,
		Type:        protocol.ProviderCreateRequestType(providerType),
		Endpoint:    endpoint,
		Model:       client.StrPtr(model),
		Temperature: parseFloat(tempStr),
		MaxTokens:   parseInt(maxTokensStr),
		ApiKey:      client.StrPtr(apiKey),
	}
	if extra != "" {
		extraMap := parseExtra(extra)
		reqTyped.Extra = &extraMap
	}

	// Pre-create verification: test the proposed config before persisting it.
	provCfg := &config.ProviderConfig{
		Name:     name,
		Type:     providerType,
		Endpoint: endpoint,
		Model:    model,
		ApiKey:   apiKey,
	}
	if tempStr != "" {
		v, err := strconv.ParseFloat(tempStr, 64)
		if err != nil {
			return fmt.Errorf("invalid temperature '%s': %w", tempStr, err)
		}
		provCfg.Temperature = v
	}
	if maxTokensStr != "" {
		v, err := strconv.Atoi(maxTokensStr)
		if err != nil {
			return fmt.Errorf("invalid max_tokens '%s': %w", maxTokensStr, err)
		}
		provCfg.MaxTokens = v
	}
	if err := verifyConfig(ctx, provCfg); err != nil {
		return fmt.Errorf("provider verification failed — %w", err)
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

	if parsed.JSON201 == nil {
		errMsg := string(parsed.Body)
		if errMsg == "" {
			return fmt.Errorf("provider creation failed with HTTP %d", resp.StatusCode)
		}
		return fmt.Errorf("provider creation failed — %s", strings.TrimSpace(errMsg))
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

	c, err := client.GetProtocolClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to create API client: %v\n", err)
		os.Exit(1)
	}

	resp, err := c.GetProvider(ctx, client.UrlPathEscape(name))
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

	fmt.Printf("Valid types: %s\n", strings.Join(validProviderTypes, ", "))

	providerTypeStr := promptWithCurrent("Provider type", client.StrPtrVal(current.Type))
	endpoint := promptWithCurrent("Endpoint URL", client.StrPtrVal(current.Endpoint))

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
		c, err := client.GetProtocolClient()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to create API client: %v\n", err)
		} else {
			resp, err := c.ListProviderModels(ctx, client.UrlPathEscape(name))
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
				fmt.Printf("  %d. %s\n", i+1, client.StrPtrVal(m.Id))
			}
			model = promptWithCurrent("Select a model number (or press Enter to keep current)", client.StrPtrVal(current.Model))
			if model != "" {
				idx, err := strconv.Atoi(model)
				if err != nil || idx < 1 || idx > len(models) {
					fmt.Fprintf(os.Stderr, "Error: invalid selection '%s'\n", model)
					os.Exit(1)
				}
				model = client.StrPtrVal(models[idx-1].Id)
			}
		} else {
			model = promptWithCurrent("Model", client.StrPtrVal(current.Model))
		}
	} else {
		model = promptWithCurrent("Model", client.StrPtrVal(current.Model))
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
		Endpoint:    client.StrPtr(endpoint),
		Model:       client.StrPtr(model),
		Temperature: parseFloat(tempStr),
		MaxTokens:   parseInt(maxTokensStr),
		ApiKey:      client.StrPtr(apiKey),
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

	// Pre-update verification: test the proposed config before persisting it.
	provCfg := &config.ProviderConfig{
		Name:     name,
		Type:     effectiveType,
		Endpoint: effectiveEndpoint,
		Model:    model,
	}
	if apiKey != "" {
		provCfg.ApiKey = apiKey
	} else if current.ApiKey != nil && *current.ApiKey != "" {
		provCfg.ApiKey = *current.ApiKey
	}
	if tempStr != "" {
		v, err := strconv.ParseFloat(tempStr, 64)
		if err != nil {
			return fmt.Errorf("invalid temperature '%s': %w", tempStr, err)
		}
		provCfg.Temperature = v
	}
	if maxTokensStr != "" {
		v, err := strconv.Atoi(maxTokensStr)
		if err != nil {
			return fmt.Errorf("invalid max_tokens '%s': %w", maxTokensStr, err)
		}
		provCfg.MaxTokens = v
	}
	if err := verifyConfig(ctx, provCfg); err != nil {
		return fmt.Errorf("provider verification failed — %w", err)
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

	resp, err = client.UpdateProvider(ctx, client.UrlPathEscape(name), req)
	if err != nil {
		return fmt.Errorf("error updating provider '%s': %w", name, err)
	}

	updateParsed, err := protocol.ParseUpdateProviderResponse(resp)
	if err != nil {
		return fmt.Errorf("error parsing response: %w", err)
	}

	if updateParsed.JSON200 == nil {
		errMsg := string(updateParsed.Body)
		if errMsg == "" {
			return fmt.Errorf("provider update failed with HTTP %d", resp.StatusCode)
		}
		return fmt.Errorf("provider update failed — %s", strings.TrimSpace(errMsg))
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
	fmt.Printf("    type:       %v\n", client.StrPtrVal(p.Type))
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

// verifyConfig tests a proposed provider configuration by calling the server's
// /api/providers/verify endpoint (which handles all LLM interactions internally).
func verifyConfig(ctx context.Context, provCfg *config.ProviderConfig) error {
	// Build the protocol.ProviderConfig request body.
	cfg := protocol.ProviderConfig{
		Name:     provCfg.Name,
		Type:     protocol.ProviderConfigType(provCfg.Type),
		Endpoint: provCfg.Endpoint,
	}
	if provCfg.Model != "" {
		cfg.Model = strPtr(provCfg.Model)
	}
	if provCfg.Temperature > 0 {
		v := float32(provCfg.Temperature)
		cfg.Temperature = &v
	}
	if provCfg.MaxTokens > 0 {
		cfg.MaxTokens = &provCfg.MaxTokens
	}
	if provCfg.ApiKey != "" {
		cfg.ApiKey = &provCfg.ApiKey
	}
	if len(provCfg.Extra) > 0 {
		extra := make(map[string]interface{})
		for k, v := range provCfg.Extra {
			extra[k] = v
		}
		cfg.Extra = &extra
	}

	resp, err := client.VerifyProviderConfig(ctx, cfg)
	if err != nil {
		return fmt.Errorf("verification request failed — %w", err)
	}

	parsed, err := protocol.ParseVerifyProviderConfigResponse(resp)
	if err != nil {
		return fmt.Errorf("failed to parse verification response: %w", err)
	}

	result := parsed.GetJSON200()
	if result == nil || result.Details == nil {
		errMsg := strings.TrimSpace(string(parsed.Body))
		if errMsg != "" {
			return fmt.Errorf("verification failed — %s", errMsg)
		}
		return fmt.Errorf("verification returned no result")
	}

	details := result.Details

	// Print verification results.
	fmt.Printf("  [✓] Model list:   ")
	if details.ModelListStatus != nil {
		switch *details.ModelListStatus {
		case "success":
			modelCount := 0
			if details.ModelCount != nil {
				modelCount = *details.ModelCount
			}
			fmt.Printf("%d models found\n", modelCount)
		case "failure":
			if details.ModelListError != nil {
				fmt.Printf("failed — %s\n", *details.ModelListError)
			} else {
				fmt.Println("failed")
			}
		default:
			fmt.Println("skipped")
		}
	} else {
		fmt.Println("skipped")
	}

	if details.CompletionStatus != nil {
		switch *details.CompletionStatus {
		case "success":
			tokens := 0
			if details.TokensUsed != nil {
				tokens = *details.TokensUsed
			}
			fmt.Printf("  [✓] Completion:   OK (%d tokens)\n", tokens)
		case "failure":
			if details.CompletionError != nil {
				fmt.Printf("  [✗] Completion:   failed — %s\n", *details.CompletionError)
			} else {
				fmt.Println("  [✗] Completion:   failed")
			}
		default:
			fmt.Println("  [ ] Completion:   skipped")
		}
	}

	// Check overall status.
	if result.Status != nil && *result.Status == "failed" {
		return fmt.Errorf("provider verification failed — %s", getVerificationError(details))
	}

	return nil
}

func getVerificationError(d *protocol.VerifyResult) string {
	var parts []string
	if d.ModelListStatus != nil && *d.ModelListStatus == "failure" {
		if d.ModelListError != nil {
			parts = append(parts, "model list: "+*d.ModelListError)
		} else {
			parts = append(parts, "model list: failed")
		}
	}
	if d.CompletionStatus != nil && *d.CompletionStatus == "failure" {
		if d.CompletionError != nil {
			parts = append(parts, "completion: "+*d.CompletionError)
		} else {
			parts = append(parts, "completion: failed")
		}
	}
	if len(parts) == 0 {
		return "unknown error"
	}
	return strings.Join(parts, "; ")
}

// handleProvidersVerify tests a provider's connectivity and runs a quick completion test.
func handleProvidersVerify(ctx context.Context, cmd *cli.Command) error {
	name := cmd.Args().First()
	if name == "" {
		fmt.Fprintln(os.Stderr, "Error: usage: norman providers verify <name>")
		return fmt.Errorf("usage: norman providers verify <name>")
	}

	c, err := client.GetProtocolClient()
	if err != nil {
		return fmt.Errorf("failed to create API client: %w", err)
	}

	resp, err := c.VerifyProvider(ctx, client.UrlPathEscape(name))
	if err != nil {
		return fmt.Errorf("error verifying provider '%s': %w", name, err)
	}

	parsed, err := protocol.ParseVerifyProviderResponse(resp)
	if err != nil {
		return fmt.Errorf("error parsing response: %w", err)
	}

	result := parsed.GetJSON200()
	if result == nil || result.Details == nil {
		errMsg := strings.TrimSpace(string(parsed.Body))
		if errMsg != "" {
			return fmt.Errorf("verify failed for provider '%s' — %s", name, errMsg)
		}
		fmt.Fprintf(os.Stderr, "Error: unexpected empty response for provider '%s' (raw body: %q)\n", name, string(parsed.Body))
		return fmt.Errorf("unexpected empty response")
	}

	details := result.Details
	fmt.Printf("\n=== Provider Verification: %s ===\n\n", strVal(result.ProviderName))

	// Model list status
	if details.ModelListStatus != nil {
		status := string(*details.ModelListStatus)
		switch status {
		case "success":
			fmt.Printf("  [✓] Model list:   %d models found\n", intPtrVal(details.ModelCount))
		case "failure":
			fmt.Printf("  [✗] Model list:   failed — %s\n", client.StrPtrVal(details.ModelListError))
		case "skipped":
			fmt.Printf("  [ ] Model list:   skipped\n")
		default:
			fmt.Printf("  [?] Model list:   %s\n", status)
		}
	}

	// Completion status
	if details.CompletionStatus != nil {
		status := string(*details.CompletionStatus)
		switch status {
		case "success":
			tokens := intPtrVal(details.TokensUsed)
			fmt.Printf("  [✓] Completion:   OK (%d tokens)\n", tokens)
		case "failure":
			fmt.Printf("  [✗] Completion:   failed — %s\n", client.StrPtrVal(details.CompletionError))
		case "skipped":
			fmt.Printf("  [ ] Completion:   skipped (no model configured or model list failed)\n")
		default:
			fmt.Printf("  [?] Completion:   %s\n", status)
		}
	}

	// Overall status
	if result.Status != nil {
		overall := string(*result.Status)
		if overall == "ok" {
			fmt.Printf("\n  ✅ Provider '%s' is healthy.\n", strVal(result.ProviderName))
		} else {
			fmt.Printf("\n  ❌ Provider '%s' verification FAILED.\n", strVal(result.ProviderName))
		}
	}

	return nil
}

// --- Helper for int pointer dereference ---
func intPtrVal(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}
