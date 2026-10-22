package config

import "fmt"

// ServerHelpText returns the help text for the server binary (norman-server).
func ServerHelpText() string {
	ed := EmbeddedDefaults()
	return fmt.Sprintf(`Usage: norman-server [OPTIONS]

Options:
  -p, --provider NAME   Select a configured provider (overrides default_provider)
  -v, --verbose         Enable server-side debug logging (Echo middleware)
  -h, --help            Show this help text and exit

Environment variables:
  NORMAN_STORE_DIR       Path to the memory store directory (default: $HOME/.norman/store)
  NORMAN_RAW_SOURCES_DIR Path to the raw sources store directory (default: $HOME/.norman/raw_sources)
  NORMAN_HOST            Server bind address (default: "%s")
  NORMAN_PORT            Server port number (default: %d)

Config file: ~/.norman/config.yaml

System defaults (overridable in config.yaml):
  default_model      %s
  server_port        %d
  host               %s
  base_dir           %s
  temperature        %.1f
  max_tokens         %d
  skills.discovery   ~/.norman/skills/, /usr/share/norman/skills/
  skills.max_active  %d
  notify.bell_enabled true
  notify.rate_limit_per_min %d

OpenShell: auto-resolved from OPENSHELL_GATEWAY_ADDRESS env var or
           metadata.json gateway_endpoint (falls back to localhost:8090).

Workspace-level overrides (per-workspace in config.yaml):
  base_dir       %s
  model          (empty = use system default)
  max_tokens     (0 = use system default)
  temperature    (0 = use system default)
  skill_overrides []
  system_prompt  (empty = use system default)

`+
		"  -p, --provider NAME   Select a configured provider (overrides default_provider)\n\n" +
		"Providers are defined in config.yaml under `llm_providers:`.\n" +
		"Example: norman-server -p local --verbose\n",
		ed.System.Host,
		ed.System.ServerPort,
		ed.System.DefaultModel,
		ed.System.ServerPort,
		ed.System.Host,
		ed.System.BaseDir,
		ed.System.Temperature,
		ed.System.MaxTokens,
		ed.System.Skills.MaxActive,
		ed.System.Notify.RateLimitPerMin,
		ed.System.BaseDir,
	)
}

// ClientHelpText returns the help text for the CLI binary (norman).
func ClientHelpText() string {
	return `Norman CLI — Session-centric chat agent

Usage: norman [-v|--verbose] [-p|--provider NAME] <command> [args...]

Flags:
  -p, --provider NAME  Select a configured LLM provider (overrides default)
  -v, --verbose        Enable verbose/debug output (request details)

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

Use "norman help <command>" for detailed help on a specific command.`
}
