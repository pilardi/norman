package config

import "fmt"

// ServerHelpText returns the help text for the server binary (norman-server).
func ServerHelpText() string {
	ed := EmbeddedDefaults()
	return fmt.Sprintf(`Usage: norman-server [OPTIONS]

Options:
  -p, --provider NAME   Select a configured provider (overrides default)
  -v, --verbose         Enable server-side debug logging (Echo middleware)
  -h, --help            Show this help text and exit

Environment variables (take priority over .env files and config.yaml):
%s
Config file: ~/.norman/config.yaml

System defaults (overridable in config.yaml):
  server_port        %d
  host               %s
  server_url         %s
  base_dir           %s
  skills.discovery   ~/.norman/skills/, /usr/share/norman/skills/
  skills.max_active  %d
  notify.bell_enabled true
  notify.rate_limit_per_min %d

OpenShell: auto-resolved from OPENSHELL_GATEWAY_ADDRESS env var or
           metadata.json gateway_endpoint (falls back to localhost:8090).

Workspace-level overrides (per-workspace in config.yaml):
  model          (empty = use provider default)
  max_tokens     (0 = use provider default)
  temperature    (0 = use provider default)
  skill_overrides []
  system_prompt  (empty = use system default)

`+
		"  -p, --provider NAME   Select a configured provider (overrides default)\n\n" +
		"Providers are defined in config.yaml under `llm_providers:`.\n" +
		"Each provider defines its own model, temperature, max_tokens, and api_key.\n" +
		"Example: norman-server -p local --verbose\n",
		BuildEnvVarHelpText(),
		ed.System.ServerPort,
		ed.System.Host,
		ed.System.ServerURL,
		ed.System.BaseDir,
		ed.System.Skills.MaxActive,
		ed.System.Notify.RateLimitPerMin,
	)
}

// ClientHelpText returns the help text for the CLI binary (norman).
func ClientHelpText() string {
	return `Norman CLI — Session-centric chat agent

Usage: norman [-v|--verbose] [-p|--provider NAME] <command> [args...]

Flags:
  -p, --provider NAME  Select a configured LLM provider (overrides default)
  -v, --verbose        Enable verbose/debug output (request details)

Environment variables (take priority over .env files and config.yaml):
` + ClientEnvVarHelpText() + `

Commands:
  config                     Show or edit current configuration
  workspaces create <name>   Create a new workspace directory
  workspaces list            List available workspaces and their sessions
  skills list                List installed skills
  skills install <id>        Install a skill from registry
  skills remove <id>         Remove an installed skill
  sessions list              List active chat sessions
  sessions archive <session_id>   Archive a session's transcript via POST /api/sessions/{id}
  sessions open <workspace_name> [<session_id>]   Open an interactive REPL chat session
  provider add <name> --type <t> --endpoint <url> [--model <m>] [--temp <f>] [--max-tokens <i>] [--api-key <k>]  Add a new LLM provider
  provider list (ls)                               List all configured providers
  provider get <name>                              Show details for a specific provider
  provider update <name> --flag value              Update an existing provider's fields
  provider delete <name>                           Remove a provider

Use "norman help <command>" for detailed help on a specific command.`
}
