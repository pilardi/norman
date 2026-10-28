package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// handleProviders implements the `norman provider` subcommands.
// Usage: norman providers add <name> --type <type> --endpoint <url> [flags...]
//        norman providers list
// parseExtra parses "key=val,key2=val2" into a map[string]string.
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
			result[pair] = true
		} else {
			key := strings.TrimSpace(pair[:idx])
			val := strings.TrimSpace(pair[idx+1:])
			if key != "" {
				result[key] = val
			}
		}
	}
	return result
}

func handleProviders(args []string) {
	if len(args) == 0 {
		fmt.Println("HELP CASE HIT")
		printprovidersHelp()
		os.Exit(1)
	}

	command := args[0]
	subArgs := args[1:]

	switch command {
	case "add":
		if len(subArgs) == 0 {
			handleProvidersAddInteractive()
		} else {
			handleProvidersAdd(subArgs)
		}
	case "list", "ls":
		handleProvidersList(subArgs)
	case "get":
		handleProvidersGet(subArgs)
	case "update":
		if len(subArgs) == 0 {
			handleProvidersUpdateInteractive()
		} else {
			handleProvidersUpdate(subArgs)
		}
	case "delete", "del":
		handleProvidersDelete(subArgs)
	case "help", "-h", "--help":
		printprovidersHelp()
		return
	default:
		fmt.Fprintf(os.Stderr, "Unknown provider subcommand: %s\n\n", command)
		fmt.Println("HELP CASE HIT")
		printprovidersHelp()
		os.Exit(1)
	}
}

// printprovidersHelp displays help for the `norman provider` subcommands.
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

Examples:
  norman providers add local --type ollama --endpoint http://localhost:11434 --model llama3
  norman providers add openai-default --type openai --endpoint https://api.openai.com/v1 --model gpt-4o --temp 0.7 --max-tokens 8192
  norman providers list
  norman providers get local
  norman providers update local --temp 0.5
  norman providers delete old-provider`)
}

// handleProvidersAdd adds a new LLM provider via POST /api/providers.
func handleProvidersAdd(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Error: provider name is required")
		os.Exit(1)
	}

	name := args[0]
	args = args[1:]

	req := make(map[string]interface{})
	req["name"] = name

	for i := 0; i < len(args); i++ {
		if args[i] == "--type" && i+1 < len(args) {
			req["type"] = args[i+1]
			i++
		} else if args[i] == "--endpoint" && i+1 < len(args) {
			req["endpoint"] = args[i+1]
			i++
		} else if args[i] == "--model" && i+1 < len(args) {
			req["model"] = args[i+1]
			i++
		} else if args[i] == "--temp" && i+1 < len(args) {
			tempVal, err := strconv.ParseFloat(args[i+1], 64)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: invalid temperature '%s': %v\n", args[i+1], err)
				os.Exit(1)
			}
			req["temperature"] = tempVal
			i++
		} else if args[i] == "--max-tokens" && i+1 < len(args) {
			tokensVal, err := strconv.Atoi(args[i+1])
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: invalid max_tokens '%s': %v\n", args[i+1], err)
				os.Exit(1)
			}
			req["max_tokens"] = tokensVal
			i++
		} else if args[i] == "--api-key" && i+1 < len(args) {
			req["api_key"] = args[i+1]
			i++
		} else if strings.HasPrefix(args[i], "--extra") {
			// Handle --extra key=val or --extra key val
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				req["extra"] = parseExtra(args[i+1])
				i++
			}
		} else if args[i] == "-h" || args[i] == "--help" {
			fmt.Println("HELP CASE HIT")
			printprovidersHelp()
			return
		} else {
			fmt.Fprintf(os.Stderr, "Unknown flag: %s\n", args[i])
			os.Exit(1)
		}
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

// handleProvidersAddInteractive runs an interactive prompt-based workflow for adding a provider.
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

	model := prompt("Model (optional)")
	tempStr := prompt("Temperature (optional, e.g. 0.7)")
	maxTokensStr := prompt("Max tokens (optional)")
	apiKey := prompt("API key (optional)")
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

// handleProvidersList lists all providers via GET /api/providers.
func handleProvidersList(args []string) {
	body, err := httpGet("/api/providers")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing providers: %v\n", err)
		os.Exit(1)
	}

	var providers []map[string]interface{}
	if err := json.Unmarshal(body, &providers); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing response: %v\n", err)
		os.Exit(1)
	}

	if len(providers) == 0 {
		fmt.Println("No providers configured.")
		return
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
}

// handleProvidersGet gets a specific provider by name via GET /api/providers/{name}.
func handleProvidersGet(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Error: provider name is required")
		os.Exit(1)
	}

	name := args[0]
	body, err := httpGet("/api/providers/" + urlPathEscape(name))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error fetching provider '%s': %v\n", name, err)
		os.Exit(1)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(body, &resp); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing response: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Provider '%s':\n", name)
	printprovidersSummary(resp)
}

// handleProvidersUpdate updates an existing provider via PUT /api/providers/{name}.
func handleProvidersUpdate(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Error: provider name is required")
		os.Exit(1)
	}

	name := args[0]
	args = args[1:]

	req := make(map[string]interface{})

	for i := 0; i < len(args); i++ {
		if args[i] == "--type" && i+1 < len(args) {
			req["type"] = args[i+1]
			i++
		} else if args[i] == "--endpoint" && i+1 < len(args) {
			req["endpoint"] = args[i+1]
			i++
		} else if args[i] == "--model" && i+1 < len(args) {
			req["model"] = args[i+1]
			i++
		} else if args[i] == "--temp" && i+1 < len(args) {
			tempVal, err := strconv.ParseFloat(args[i+1], 64)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: invalid temperature '%s': %v\n", args[i+1], err)
				os.Exit(1)
			}
			req["temperature"] = tempVal
			i++
		} else if args[i] == "--max-tokens" && i+1 < len(args) {
			tokensVal, err := strconv.Atoi(args[i+1])
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: invalid max_tokens '%s': %v\n", args[i+1], err)
				os.Exit(1)
			}
			req["max_tokens"] = tokensVal
			i++
		} else if args[i] == "--api-key" && i+1 < len(args) {
			req["api_key"] = args[i+1]
			i++
		} else if strings.HasPrefix(args[i], "--extra") {
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				req["extra"] = parseExtra(args[i+1])
				i++
			}
		} else if args[i] == "-h" || args[i] == "--help" {
			fmt.Println("HELP CASE HIT")
			printprovidersHelp()
			return
		} else {
			fmt.Fprintf(os.Stderr, "Unknown flag: %s\n", args[i])
			os.Exit(1)
		}
	}

	if len(req) == 0 {
		fmt.Fprintln(os.Stderr, "Error: no fields to update. Use --flag value syntax.")
		os.Exit(1)
	}

	url := "/api/providers/" + urlPathEscape(name)
	body, err := httpPutJSON(url, req)
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

	providerType := promptWithCurrent("Provider type", current["type"])
	endpoint := promptWithCurrent("Endpoint URL", current["endpoint"])
	model := promptWithCurrent("Model", current["model"])

	tempVal := current["temperature"]
	tempStr := promptWithCurrent("Temperature", fmt.Sprintf("%.2f", tempVal))
	maxTokensStr := promptWithCurrent("Max tokens", current["max_tokens"])
	apiKey := promptWithCurrent("API key", current["api_key"])
	extra := promptWithCurrent("Extra (key=val)", current["extra"])

	// Build the request map (only non-empty fields)
	req := make(map[string]interface{})

	if providerType != "" {
		req["type"] = providerType
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

// handleProvidersDelete deletes a provider via DELETE /api/providers/{name}.
func handleProvidersDelete(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Error: provider name is required")
		os.Exit(1)
	}

	name := args[0]
	err := httpDelete("/api/providers/" + urlPathEscape(name))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error deleting provider '%s': %v\n", name, err)
		os.Exit(1)
	}

	fmt.Printf("Provider '%s' removed.\n", name)
}

// printprovidersSummary prints a formatted summary of a provider response.
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
