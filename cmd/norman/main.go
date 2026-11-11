// Package main implements the Norman CLI client using urfave/cli/v3.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/urfave/cli/v3"

	"github.com/norman/norman/pkg/version"
)

var (
	selectedProvider string = ""
)

func main() {
	app := &cli.Command{
		EnableShellCompletion: true,
		Before: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
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
- environment variables (NORMAN_HOST, NORMAN_PORT, NORMAN_SERVER_URL, NORMAN_BASE_DIR)
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
						err := handleConfigSet(ctx, []string{setVal})
						if err != nil {
							fmt.Fprintf(os.Stderr, "Error: %v\n", err)
							return err
						}
						return nil
					}
					return handleConfig(ctx, cmd)
				},
			},

			// --- version subcommand ---
			{
				Name:        "version",
				Aliases:     []string{"ver"},
				Usage:       "Show version and build date",
				Description: `Prints the build version (from git describe) and build timestamp.`,
				Action: func(ctx context.Context, cmd *cli.Command) error {
					fmt.Fprintf(os.Stdout, "%s\n", version.String("norman"))
					return nil
				},
			},
			// --- providers subcommand ---
			{
				Name:  "providers",
				Usage: "Manage LLM providers (add, list, get, update, delete, verify)",
				Description: `provider — Manage LLM providers (add, list, get, update, delete, verify)

Examples:
  norman providers add local --type ollama --endpoint http://localhost:11434 --model llama3
  norman providers add openai-default --type openai --endpoint https://api.openai.com/v1 --model gpt-4o --temp 0.7 --max-tokens 8192
  norman providers list
  norman providers get local
  norman providers update local --temp 0.5
  norman providers delete old-provider
  norman providers set-default local
  norman providers list-models local
  norman providers verify local`,
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
					{
						Name:    "verify",
						Aliases: []string{"test"},
						Usage:   "Verify a provider's connectivity and run a quick completion test",
						Description: `providers verify — Test a provider's API connectivity and run a minimal completion

Tests both model listing and a quick chat completion against the provider's endpoint.
Reports connectivity status, model count, and completion result with token usage.

Examples:
  norman providers verify local-llamacpp
  norman providers verify unsloth`,
						Action: func(ctx context.Context, cmd *cli.Command) error {
							if cmd.Args().Len() < 1 {
								fmt.Fprintf(os.Stderr, "Error: usage: norman providers verify <name>\n")
								return fmt.Errorf("usage: norman providers verify <name>")
							}
							err := handleProvidersVerify(ctx, cmd)
							if err != nil {
								fmt.Fprintf(os.Stderr, "Error: %v\n", err)
								return err
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
				Action: func(ctx context.Context, cmd *cli.Command) error {
					cli.HelpPrinter(os.Stdout, cli.SubcommandHelpTemplate, cmd)
					return nil
				},
				Commands: []*cli.Command{
					{
						Name:    "list",
						Aliases: []string{"ls"},
						Usage:   "List active chat sessions, optionally filtered by workspace name",
						Description: `Without a workspace argument, lists all sessions across all workspaces.
Pass a workspace name (e.g., 'norman sessions ls test-ws') to show only sessions for that workspace.`,
						Action: func(ctx context.Context, cmd *cli.Command) error {
							err := handleSessionList(cmd.Args().Slice())
							if err != nil {
								fmt.Fprintf(os.Stderr, "Error: %v\n", err)
							}
							return err
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
					{
						Name:  "history",
						Usage: "Show conversation history for a session",
						Description: `sessions history — Show the last N messages of a session

Usage: norman sessions history <session_id_or_alias> [N] [--json]

Options:
  --json    Output as machine-readable JSON instead of human-readable format

Examples:
  norman sessions history ab953ff5          # Last 10 messages (default)
  norman sessions history ab953ff5 20      # Last 20 messages
  norman sessions history my-session --json  # Raw JSON output`,
						Flags: []cli.Flag{
							&cli.BoolFlag{
								Name:  "json",
								Usage: "Output as machine-readable JSON instead of human-readable format",
							},
						},
						Action: func(ctx context.Context, cmd *cli.Command) error {
							args := cmd.Args().Slice()
							if len(args) == 0 {
								fmt.Fprintf(os.Stderr, "Error: usage: norman sessions history <session_id_or_alias> [N] [--json]\n")
								return fmt.Errorf("usage: norman sessions history <session_id_or_alias> [N] [--json]")
							}

							var sessionID string
							n := 10
							for _, arg := range args {
								if sessionID == "" && !strings.HasPrefix(arg, "-") {
									sessionID = arg
								} else if num, _ := fmt.Sscanf(arg, "%d", &n); num > 0 {
									n = num
								}
							}

							if sessionID == "" {
								fmt.Fprintf(os.Stderr, "Error: session ID or alias is required\n")
								return fmt.Errorf("usage: norman sessions history <session_id_or_alias> [N] [--json]")
							}

							var extraArgs []string
							extraArgs = append(extraArgs, fmt.Sprintf("%d", n))
							if cmd.Bool("json") {
								extraArgs = append(extraArgs, "--json")
							}
							showHistory(sessionID, extraArgs)
							return nil
						},
					},
				},
			},
			// --- skills subcommand ---
			{
				Name:  "skills",
				Usage: "Manage installed skills",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					cli.HelpPrinter(os.Stdout, cli.SubcommandHelpTemplate, cmd)
					return nil
				},
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
						Aliases: []string{"rmt"},
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
			// --- workspaces subcommand ---
			{
				Name:  "workspaces",
				Usage: "Manage workspaces",
				Description: `workspaces — Manage workspaces

Usage: norman workspaces <subcommand> [args...]`,
				Commands: []*cli.Command{
					{
						Name:  "create",
						Usage: "Create a new workspace directory",
						Description: `Creates a new workspace directory and registers it with the agent server.
The workspace name must be alphanumeric (underscores and hyphens allowed).
Purpose can be provided inline (--purpose "text"), from a file (-f <path>), or piped via stdin.
Tags can be set with --tags "tag1,tag2" or repeated: --tags tag1 --tags tag2.`,
						Flags: []cli.Flag{
							&cli.StringFlag{
								Name:  "purpose",
								Usage: "Inline purpose description (short text)",
							},
							&cli.StringFlag{
								Name:    "file",
								Aliases: []string{"f"},
								Usage:   "Read purpose from a local text/markdown file",
							},
							&cli.StringSliceFlag{
								Name:  "tags",
								Usage: "Domain tags for skill activation matching (e.g., grpc,migration)",
							},
						},
						Action: func(ctx context.Context, cmd *cli.Command) error {
							purpose := ""

							// Priority: --purpose > -f > stdin pipe.
							if p := cmd.String("purpose"); p != "" {
								purpose = p
							} else if f := cmd.String("file"); f != "" {
								data, err := os.ReadFile(f)
								if err != nil {
									return fmt.Errorf("failed to read purpose file %q: %v", f, err)
								}
								purpose = strings.TrimSpace(string(data))
							} else if !term.IsTerminal(int(os.Stdin.Fd())) {
								data, err := io.ReadAll(os.Stdin)
								if err != nil {
									return fmt.Errorf("failed to read stdin: %v", err)
								}
								purpose = strings.TrimSpace(string(data))
							}

							var tags []string
							if t := cmd.StringSlice("tags"); len(t) > 0 {
								tags = t
							}

							err := handleWorkspacesCreate(ctx, cmd.Args().Slice(), purpose, tags)
							if err != nil {
								fmt.Fprintf(os.Stderr, "Error: %v\n", err)
							}
							return err
						},
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
							err := handleWorkspaceStop(ctx, cmd.Args().Slice())
							if err != nil {
								fmt.Fprintf(os.Stderr, "Error: %v\n", err)
							}
							return err
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
							err := handleWorkspaceRestore(ctx, cmd.Args().Slice())
							if err != nil {
								fmt.Fprintf(os.Stderr, "Error: %v\n", err)
							}
							return err
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
							err := handleWorkspacePurge(ctx, cmd.Args().Slice(), cmd.Bool("confirm"))
							if err != nil {
								fmt.Fprintf(os.Stderr, "Error: %v\n", err)
							}
							return err
						},
						Flags: []cli.Flag{
							&cli.BoolFlag{
								Name:  "confirm",
								Usage: "Confirm the destructive operation",
							},
						},
					},
					{
						Name:  "update",
						Usage: "Update a workspace's purpose or tags",
						Description: `Partially updates a workspace. Only provided fields are changed;
omitted fields retain their current values.
Purpose can be provided inline (--purpose "text"), from a file (-f <path>), or piped via stdin.`,
						Flags: []cli.Flag{
							&cli.StringFlag{
								Name:  "purpose",
								Usage: "Inline purpose description (replaces existing)",
							},
							&cli.StringFlag{
								Name:    "file",
								Aliases: []string{"f"},
								Usage:   "Read purpose from a local text/markdown file",
							},
							&cli.StringSliceFlag{
								Name:  "tags",
								Usage: "Domain tags for skill activation matching (replaces existing)",
							},
						},
						Action: func(ctx context.Context, cmd *cli.Command) error {
							var purpose string

							// Priority: --purpose > -f > stdin pipe.
							if p := cmd.String("purpose"); p != "" {
								purpose = p
							} else if f := cmd.String("file"); f != "" {
								data, err := os.ReadFile(f)
								if err != nil {
									return fmt.Errorf("failed to read purpose file %q: %v", f, err)
								}
								purpose = strings.TrimSpace(string(data))
							} else if !term.IsTerminal(int(os.Stdin.Fd())) {
								data, err := io.ReadAll(os.Stdin)
								if err != nil {
									return fmt.Errorf("failed to read stdin: %v", err)
								}
								purpose = strings.TrimSpace(string(data))
							}

							var tags []string
							if t := cmd.StringSlice("tags"); len(t) > 0 {
								tags = t
							}
							err := handleWorkspaceUpdate(ctx, cmd.Args().Slice(), purpose, tags)
							if err != nil {
								fmt.Fprintf(os.Stderr, "Error: %v\n", err)
							}
							return err
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
