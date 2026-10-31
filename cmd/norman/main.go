// Package main implements the Norman CLI client using urfave/cli/v3.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/urfave/cli/v3"
)

var (
	verbose          bool   = false
	selectedProvider string = ""
)

func main() {
	app := &cli.Command{
		EnableShellCompletion: true,
		Before: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
			if cmd.Bool("verbose") {
				verbose = true
			}
			if cmd.String("provider") != "" {
				selectedProvider = cmd.String("provider")
			}
			return ctx, nil
		},
		Name:  "norman",
		Usage: "Norman CLI — Session-centric chat agent",
		Description: `Norman CLI — Session-centric chat agent

Usage: norman [-v|--verbose] [-p|--provider NAME] <command> [args...]

Configuration is loaded from three sources (priority order):
- environment variables (NORMAN_HOST, NORMAN_PORT, NORMAN_SERVER_URL)
- .env files (~/.norman/.env, ././.env)
- config.yaml (~/.config/norman/config.yaml)

Use 'norman config --json' to inspect the effective configuration.`,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "verbose",
				Aliases: []string{"v"},
				Usage:   "Enable verbose/debug output (request details)",
			},
			&cli.StringFlag{
				Name:    "provider",
				Aliases: []string{"p"},
				Usage:   "Select a configured LLM provider (overrides default)",
			},
		},
		Commands: []*cli.Command{
			// --- config subcommand ---
			{
				Name:  "config",
				Usage: "Show or edit current configuration",
				Description: `config — Show or edit configuration

Usage: norman config [--set key=value | --json]

Options:
  --set     Set a config value (e.g., --set server_port=9090)
            For providers use dot-path notation: --set providers.0.name=myprovider
            Use --set default_provider=<name> to set the default provider.
  --json    Output as machine-readable JSON

Config sources (priority): env vars > .env files (~/.norman/.env, ././.env) > config.yaml (~/.config/norman/config.yaml) > embedded defaults.
Secrets are stored as ${secret:...} placeholders and resolved by OpenShell.`,
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:  "json",
						Usage: "Output configuration as JSON",
					},
					&cli.StringFlag{
						Name:  "set",
						Usage: "Set a config value (e.g., --set server_port=9090)",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					if cmd.Bool("json") {
						return handleConfigJSON(ctx, cmd)
					}
					if setVal := cmd.String("set"); setVal != "" {
						return handleConfigSet(ctx, []string{setVal})
					}
					return handleConfig(ctx, cmd)
				},
			},
			// --- workspaces subcommand ---
			{
				Name:  "workspaces",
				Usage: "Manage workspaces",
				Description: `workspaces — Manage workspaces

Usage: norman workspaces <subcommand> [args...]

Commands:
  create <name>       Create a new workspace directory
  list                List available workspaces and their sessions
  stop <name>         Stop a workspace (write snapshot + free sandbox)
  restore <name>      Restore a stopped workspace to active (replay snapshot)
  purge <name>        Permanently delete a workspace (requires --confirm)`,
				Commands: []*cli.Command{
					{
						Name:  "create",
						Usage: "Create a new workspace directory",
						Description: `Creates a new workspace directory and registers it with the agent server.
The workspace name must be alphanumeric (underscores and hyphens allowed).`,
						Action: handleWorkspacesCreate,
					},
					{
						Name:        "list",
						Aliases:     []string{"ls"},
						Usage:       "List available workspaces and their sessions",
						Description: `Queries the agent server for workspace information.`,
						Action:      handleWorkspacesList,
					},
					{
						Name:  "stop",
						Usage: "Stop a workspace (write snapshot + free sandbox)",
						Action: func(ctx context.Context, cmd *cli.Command) error {
							return handleWorkspaceStop(ctx, cmd.Args().Slice())
						},
						Flags: []cli.Flag{
							&cli.StringFlag{
								Name:  "reason",
								Usage: "Reason for stopping",
							},
						},
					},
					{
						Name:  "restore",
						Usage: "Restore a stopped workspace to active (replay snapshot)",
						Action: func(ctx context.Context, cmd *cli.Command) error {
							return handleWorkspaceRestore(ctx, cmd.Args().Slice())
						},
						Flags: []cli.Flag{
							&cli.BoolFlag{
								Name:  "no-recreate",
								Usage: "Do not recreate sandbox on restore (default: true)",
							},
						},
					},
					{
						Name:  "purge",
						Usage: "Permanently delete a workspace (requires --confirm)",
						Action: func(ctx context.Context, cmd *cli.Command) error {
							return handleWorkspacePurge(ctx, cmd.Args().Slice())
						},
						Flags: []cli.Flag{
							&cli.BoolFlag{
								Name:  "confirm",
								Usage: "Confirm the destructive operation",
							},
						},
					},
				},
			},
			// --- skills subcommand ---
			{
				Name:  "skills",
				Usage: "Manage installed skills",
				Description: `skills — Manage installed skills

Commands:
  list       Show installed skills with provenance
  install <path|repo>  Install a skill from a local path or git repo URL
  remote <git-url>     Install a skill from a git repository (alias for install)
  remove <name>   Remove an installed skill by name`,
				Action: handleSkills,
				Commands: []*cli.Command{
					{
						Name:    "list",
						Aliases: []string{"ls"},
						Usage:   "Show installed skills with provenance",
						Action:  listSkills,
					},
					{
						Name:  "install",
						Usage: "Install a skill from a local path or git repo URL",
						Action: func(ctx context.Context, cmd *cli.Command) error {
							if cmd.Args().Len() < 1 {
								fmt.Fprintf(os.Stderr, "Error: usage: norman skills install <path|repo>\n")
								return fmt.Errorf("usage: norman skills install <path|repo>")
							}
							err := installSkill(ctx, cmd.Args().First())
							if err != nil {
								fmt.Fprintf(os.Stderr, "Error: install failed: %v\n", err)
								return fmt.Errorf("install failed: %v", err)
							}
							return nil
						},
					},
					{
						Name:    "remote",
						Aliases: []string{"rm"},
						Usage:   "Install a skill from a git repository URL",
						Action: func(ctx context.Context, cmd *cli.Command) error {
							if cmd.Args().Len() < 1 {
								fmt.Fprintf(os.Stderr, "Error: usage: norman skills remote <git-url> [version]\n")
								return fmt.Errorf("usage: norman skills remote <git-url> [version]")
							}
							err := installRemote(ctx, cmd.Args().First())
							if err != nil {
								fmt.Fprintf(os.Stderr, "Error: remote install failed: %v\n", err)
								return fmt.Errorf("remote install failed: %v", err)
							}
							return nil
						},
					},
					{
						Name:  "remove",
						Usage: "Remove an installed skill by name",
						Action: func(ctx context.Context, cmd *cli.Command) error {
							if cmd.Args().Len() < 1 {
								fmt.Fprintf(os.Stderr, "Error: usage: norman skills remove <name>\n")
								return fmt.Errorf("usage: norman skills remove <name>")
							}
							err := removeSkill(ctx, cmd.Args().First())
							if err != nil {
								fmt.Fprintf(os.Stderr, "Error: remove failed: %v\n", err)
								return fmt.Errorf("remove failed: %v", err)
							}
							return nil
						},
					},
				},
			},
			// --- sessions subcommand ---
			{
				Name:  "sessions",
				Usage: "Manage chat sessions",
				Description: `sessions — Manage chat sessions

Commands:
  open <workspace_name> [<session_id>]   Open an interactive REPL chat session
  list                          List active chat sessions
  archive <session_id>          Archive a session's transcript via POST /api/sessions/{id}`,
				Action: handleSessions,
				Commands: []*cli.Command{
					{
						Name:    "list",
						Aliases: []string{"ls"},
						Usage:   "List active chat sessions",
						Action: func(ctx context.Context, cmd *cli.Command) error {
							return handleSessionList(cmd.Args().Slice())
						},
					},
					{
						Name:  "archive",
						Usage: "Archive a session's transcript via POST /api/sessions/{id}",
						Action: func(ctx context.Context, cmd *cli.Command) error {
							if cmd.Args().Len() < 1 {
								fmt.Fprintf(os.Stderr, "Error: usage: norman sessions archive <session_id>\n")
								return fmt.Errorf("usage: norman sessions archive <session_id>")
							}
							err := handleSessionArchive(cmd.Args().First())
							if err != nil {
								fmt.Fprintf(os.Stderr, "Error: archive failed: %v\n", err)
								return fmt.Errorf("archive failed: %v", err)
							}
							return nil
						},
					},
					{
						Name:    "open",
						Aliases: []string{"chat"},
						Usage:   "Open an interactive REPL chat session",
						Description: `sessions open — Interactive REPL chat mode

Usage: norman sessions open <workspace_name> [<session_id>]

Slash-commands (prefix with /):
  /help             Show this help
  /tasks            List emergent scheduled tasks for current session
  /close            Close the current session (save transcript)
  /new              Start a new chat session
  /history [N]      Show last N messages of current session
  /sessions         List all active sessions
  /switch <id>      Switch to another session by ID
  /archive <id>     Archive a session's transcript by session ID

Examples:
  norman sessions open my-project
  norman sessions open my-project abc12345`,
						Action: func(ctx context.Context, cmd *cli.Command) error {
							handleChat(cmd.Args().Slice())
							return nil
						},
					},
				},
			},
			// --- providers subcommand ---
			{
				Name:  "providers",
				Usage: "Manage LLM providers (add, list, get, update, delete)",
				Description: `provider — Manage LLM providers (add, list, get, update, delete)

Commands:
  add [name] [--type <type>] [--endpoint <url>] [--model <model>] [--temp <float>] [--max-tokens <int>] [--api-key <key>] [--extra key=val]
      Add a new LLM provider. If no flags are provided, interactive prompts will be used (auto-fetches available models).
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
  norman providers list-models local`,
				Commands: []*cli.Command{
					{
						Name:  "add",
						Usage: "Add a new LLM provider",
						Action: func(ctx context.Context, cmd *cli.Command) error {
							if cmd.Args().Len() < 1 {
								fmt.Fprintf(os.Stderr, "Error: usage: norman providers add <name> [--type <type>] [--endpoint <url>] [--model <model>] [--temp <float>] [--max-tokens <int>] [--api-key <key>] [--extra key=val]\n")
								return fmt.Errorf("usage: norman providers add <name> [--type <type>] [--endpoint <url>] [--model <model>] [--temp <float>] [--max-tokens <int>] [--api-key <key>] [--extra key=val]")
							}
							err := handleProvidersAdd(ctx, cmd)
							if err != nil {
								fmt.Fprintf(os.Stderr, "Error: %v\n", err)
								return err
							}
							return nil
						},
						Flags: []cli.Flag{
							&cli.StringFlag{Name: "type", Usage: "Provider type (openai, anthropic, ollama, gemini, llamacpp)"},
							&cli.StringFlag{Name: "endpoint", Usage: "API endpoint URL"},
							&cli.StringFlag{Name: "model", Usage: "Model name"},
							&cli.Float64Flag{Name: "temp", Usage: "Temperature (0.0–2.0)"},
							&cli.IntFlag{Name: "max-tokens", Usage: "Maximum tokens per request"},
							&cli.StringFlag{Name: "api-key", Usage: "API key"},
						},
					},
					{
						Name:    "list",
						Aliases: []string{"ls"},
						Usage:   "List all configured providers with their settings",
						Action: func(ctx context.Context, cmd *cli.Command) error {
							err := handleProvidersList(ctx, cmd)
							if err != nil {
								fmt.Fprintf(os.Stderr, "Error: %v\n", err)
								return err
							}
							return nil
						},
					},
					{
						Name:  "get",
						Usage: "Show full details for a specific provider by name",
						Action: func(ctx context.Context, cmd *cli.Command) error {
							if cmd.Args().Len() < 1 {
								fmt.Fprintf(os.Stderr, "Error: usage: norman providers get <name>\n")
								return fmt.Errorf("usage: norman providers get <name>")
							}
							err := handleProvidersGet(ctx, cmd)
							if err != nil {
								fmt.Fprintf(os.Stderr, "Error: %v\n", err)
								return err
							}
							return nil
						},
					},
					{
						Name:  "update",
						Usage: "Update an existing provider's fields (partial update)",
						Action: func(ctx context.Context, cmd *cli.Command) error {
							if !cmd.IsSet("type") && !cmd.IsSet("endpoint") && !cmd.IsSet("model") &&
								!cmd.IsSet("temp") && !cmd.IsSet("max-tokens") && !cmd.IsSet("api-key") {
								name := cmd.Args().First()
								if err := handleProvidersUpdateInteractive(ctx, name); err != nil {
									fmt.Fprintf(os.Stderr, "Error: %v\n", err)
									return err
								}
								return nil
							}
							if cmd.Args().Len() < 1 {
								fmt.Fprintf(os.Stderr, "Error: usage: norman providers update <name> [--type <type>] [--endpoint <url>] [--model <model>] [--temp <float>] [--max-tokens <int>] [--api-key <key>] [--extra key=val]\n")
								return fmt.Errorf("usage: norman providers update <name> [--type <type>] [--endpoint <url>] [--model <model>] [--temp <float>] [--max-tokens <int>] [--api-key <key>] [--extra key=val]")
							}
							err := handleProvidersUpdate(ctx, cmd)
							if err != nil {
								fmt.Fprintf(os.Stderr, "Error: %v\n", err)
								return err
							}
							return nil
						},
						Flags: []cli.Flag{
							&cli.StringFlag{Name: "type", Usage: "Provider type"},
							&cli.StringFlag{Name: "endpoint", Usage: "API endpoint URL"},
							&cli.StringFlag{Name: "model", Usage: "Model name"},
							&cli.Float64Flag{Name: "temp", Usage: "Temperature (0.0–2.0)"},
							&cli.IntFlag{Name: "max-tokens", Usage: "Maximum tokens per request"},
							&cli.StringFlag{Name: "api-key", Usage: "API key"},
						},
					},
					{
						Name:    "delete",
						Aliases: []string{"del"},
						Usage:   "Remove a provider. Fails if it's the last remaining one",
						Action: func(ctx context.Context, cmd *cli.Command) error {
							if cmd.Args().Len() < 1 {
								fmt.Fprintf(os.Stderr, "Error: usage: norman providers delete <name>\n")
								return fmt.Errorf("usage: norman providers delete <name>")
							}
							err := handleProvidersDelete(ctx, cmd)
							if err != nil {
								fmt.Fprintf(os.Stderr, "Error: %v\n", err)
								return err
							}
							return nil
						},
					},
					{
						Name:    "set-default",
						Aliases: []string{"sd"},
						Usage:   "Set the default provider for sessions and CLI operations",
						Action: func(ctx context.Context, cmd *cli.Command) error {
							if cmd.Args().Len() < 1 {
								fmt.Fprintf(os.Stderr, "Error: usage: norman providers set-default <name>\n")
								return fmt.Errorf("usage: norman providers set-default <name>")
							}
							err := handleProvidersSetDefault(ctx, cmd)
							if err != nil {
								fmt.Fprintf(os.Stderr, "Error: %v\n", err)
								return err
							}
							return nil
						},
					},
					{
						Name:    "list-models",
						Aliases: []string{"lm"},
						Usage:   "List available models for a configured provider by querying its API endpoint",
						Action: func(ctx context.Context, cmd *cli.Command) error {
							if cmd.Args().Len() < 1 {
								fmt.Fprintf(os.Stderr, "Error: usage: norman providers list-models <name>\n")
								return fmt.Errorf("usage: norman providers list-models <name>")
							}
							err := handleProvidersListModels(ctx, cmd)
							if err != nil {
								fmt.Fprintf(os.Stderr, "Error: %v\n", err)
								return err
							}
							return nil
						},
					},
				},
			},
		},
	}

	if err := app.Run(context.Background(), os.Args); err != nil {
		os.Exit(1)
	}
}
