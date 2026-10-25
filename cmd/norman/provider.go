package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// handleProvider implements the `norman provider` subcommands.
// Usage: norman provider add <name> --type <type> --endpoint <url> [flags...]
//        norman provider list
func handleProvider(args []string) {
	if len(args) == 0 {
		fmt.Println("HELP CASE HIT")
		printProviderHelp()
		os.Exit(1)
	}

	command := args[0]
	subArgs := args[1:]

	switch command {
	case "add":
		handleProviderAdd(subArgs)
	case "list", "ls":
		handleProviderList(subArgs)
	case "get":
		handleProviderGet(subArgs)
	case "update":
		handleProviderUpdate(subArgs)
	case "delete", "del":
		handleProviderDelete(subArgs)
	case "help", "-h", "--help":
		printProviderHelp()
		return
	default:
		fmt.Fprintf(os.Stderr, "Unknown provider subcommand: %s\n\n", command)
		fmt.Println("HELP CASE HIT")
		printProviderHelp()
		os.Exit(1)
	}
}

// printProviderHelp displays help for the `norman provider` subcommands.
func printProviderHelp() {
	fmt.Println(`provider — Manage LLM providers (add, list, get, update, delete)

Usage: norman provider <subcommand> [args...]

Subcommands:
  add <name> --type <type> --endpoint <url> [--model <model>] [--temp <float>] [--max-tokens <int>] [--api-key <key>] [--extra key=val]
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
  norman provider add local --type ollama --endpoint http://localhost:11434 --model llama3
  norman provider add openai-default --type openai --endpoint https://api.openai.com/v1 --model gpt-4o --temp 0.7 --max-tokens 8192
  norman provider list
  norman provider get local
  norman provider update local --temp 0.5
  norman provider delete old-provider`)
}

// handleProviderAdd adds a new LLM provider via POST /api/providers.
func handleProviderAdd(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Error: provider name is required")
		os.Exit(1)
	}

	name := args[0]
	args = args[1:]

	var req map[string]interface{}
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
			req["temperature"] = args[i+1]
			i++
		} else if args[i] == "--max-tokens" && i+1 < len(args) {
			req["max_tokens"] = args[i+1]
			i++
		} else if args[i] == "--api-key" && i+1 < len(args) {
			req["api_key"] = args[i+1]
			i++
		} else if strings.HasPrefix(args[i], "--extra") {
			// Handle --extra key=val or --extra key val
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				req["extra"] = args[i+1]
				i++
			}
		} else if args[i] == "-h" || args[i] == "--help" {
			fmt.Println("HELP CASE HIT")
		printProviderHelp()
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
	printProviderSummary(resp)
}

// handleProviderList lists all providers via GET /api/providers.
func handleProviderList(args []string) {
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
		printProviderSummary(p)
		if i < len(providers)-1 {
			fmt.Println(strings.Repeat("-", 70))
		}
	}
	fmt.Println()
}

// handleProviderGet gets a specific provider by name via GET /api/providers/{name}.
func handleProviderGet(args []string) {
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
	printProviderSummary(resp)
}

// handleProviderUpdate updates an existing provider via PUT /api/providers/{name}.
func handleProviderUpdate(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Error: provider name is required")
		os.Exit(1)
	}

	name := args[0]
	args = args[1:]

	var req map[string]interface{}

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
			req["temperature"] = args[i+1]
			i++
		} else if args[i] == "--max-tokens" && i+1 < len(args) {
			req["max_tokens"] = args[i+1]
			i++
		} else if args[i] == "--api-key" && i+1 < len(args) {
			req["api_key"] = args[i+1]
			i++
		} else if strings.HasPrefix(args[i], "--extra") {
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				req["extra"] = args[i+1]
				i++
			}
		} else if args[i] == "-h" || args[i] == "--help" {
			fmt.Println("HELP CASE HIT")
		printProviderHelp()
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
	body, err := httpPostJSON(url, req)
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
	printProviderSummary(resp)
}

// handleProviderDelete deletes a provider via DELETE /api/providers/{name}.
func handleProviderDelete(args []string) {
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

// printProviderSummary prints a formatted summary of a provider response.
func printProviderSummary(p map[string]interface{}) {
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
