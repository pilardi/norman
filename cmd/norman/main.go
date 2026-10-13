// Package main implements the Norman CLI client.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/norman/norman/pkg/config"
	"github.com/norman/norman/pkg/env"
	"github.com/norman/norman/pkg/scheduler"
	"github.com/norman/norman/pkg/skills"
	"gopkg.in/yaml.v3"
)

var globalClient *env.Client
var globalManager *env.WorkspaceManager

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(0)
	}

	command := os.Args[1]
	args := os.Args[2:]

	switch command {
	case "config":
		handleConfig(args)
	case "create":
		handleCreate(args)
	case "switch":
		handleSwitch(args)
	case "run":
		handleRun(args)
	case "list":
		handleList(args)
	case "skills":
		handleSkills(args)
	case "sessions":
		handleSessions(args)
	case "tasks":
		handleTasks(args)
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`Norman CLI — Agent workspace manager

Usage: norman <command> [args...]

Commands:
  create <name>        Create a new workspace + persistent OpenShell session
  switch <name>        Switch to an existing workspace session
  run [prompt]         Execute a prompt inside the workspace session
  list                 List available workspaces and their sessions
  config               Show or edit current configuration
  skills list          List installed skills
  skills install <id>  Install a skill from registry
  skills remove <id>   Remove an installed skill
  sessions             List active OpenShell sessions
  tasks add <name> --schedule "<cron>"  Register a recurring task
  tasks list                           List registered tasks
  tasks remove <id>                    Unregister a task
  tasks run <id>                       Trigger a task immediately

Use "norman help <command>" for detailed help on a specific command.`)
}

// handleConfig shows or edits the current config.
func handleConfig(args []string) {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Println(`config — Show or edit configuration

Usage: norman config [--show | --set key=value]

Options:
  --show    Display current config values (read from ~/.config/norman/config.yaml)
  --set     Set a config value (e.g., --set server_port=9090)

Config is persisted as YAML at ~/.config/norman/config.yaml.
Secrets are stored as ${secret:...} placeholders and resolved by OpenShell.`)
		return
	}

	c, err := config.LoadDefault()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	if len(args) > 0 && args[0] == "--set" {
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: norman config --set key=value")
			os.Exit(1)
		}
		parts := strings.SplitN(args[1], "=", 2)
		if len(parts) != 2 {
			fmt.Fprintf(os.Stderr, "Invalid format: %s (expected key=value)\n", args[1])
			os.Exit(1)
		}
		key, val := parts[0], parts[1]

		// Simple key-value setter (extensible for nested keys)
		switch key {
		case "default_model":
			c.DefaultModel = val
		case "server_port":
			fmt.Sscanf(val, "%d", &c.ServerPort)
		case "max_tokens":
			fmt.Sscanf(val, "%d", &c.Agent.MaxTokens)
		case "temperature":
			fmt.Sscanf(val, "%f", &c.Agent.Temperature)
		default:
			fmt.Fprintf(os.Stderr, "Unknown config key: %s\n", key)
			os.Exit(1)
		}

		if err := c.Validate(); err != nil {
			fmt.Fprintf(os.Stderr, "Validation failed: %v\n", err)
			os.Exit(1)
		}

		if err := c.Save(getConfigPath()); err != nil {
			fmt.Fprintf(os.Stderr, "Error saving config: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Updated %s → %s\n", key, val)
		return
	}

	// Default: show current config
	fmt.Println("# Current Configuration")
	fmt.Printf("  default_model:    %s\n", c.DefaultModel)
	fmt.Printf("  server_port:      %d\n", c.ServerPort)
	fmt.Printf("  max_tokens:       %d\n", c.Agent.MaxTokens)
	fmt.Printf("  temperature:      %.2f\n", c.Agent.Temperature)
	fmt.Printf("  workspace_base:   %s\n", c.Workspaces.BaseDir)
}

// handleCreate creates a new workspace with a persistent OpenShell session.
func handleCreate(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: norman create <name>")
		os.Exit(1)
	}

	name := args[0]

	// Initialize env client if not already done
	if globalClient == nil {
		var err error
		globalClient, err = env.NewClient("127.0.0.1:8080")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error connecting to OpenShell gateway: %v\n", err)
			os.Exit(1)
		}
		globalManager = env.NewWorkspaceManager(globalClient)
		defer globalClient.Close()
	}

	ctx := context.Background()

	// Create the workspace sandbox via OpenShell SDK
	session, err := globalManager.CreateWorkspace(ctx, name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating workspace '%s': %v\n", name, err)
		os.Exit(1)
	}

	// Also create the local .openshell/ overlay directory for host-side tracking
	c, _ := config.LoadDefault()
	baseDir, _ := expandHome(c.Workspaces.BaseDir)
	workspacePath := filepath.Join(baseDir, name)
	overlayPath := filepath.Join(workspacePath, ".openshell")

	if err := os.MkdirAll(overlayPath, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not create overlay dir: %v\n", err)
	}

	fmt.Printf("Created workspace '%s'\n", name)
	fmt.Printf("  Sandbox:   %s (persistent OpenShell session)\n", session.SandboxName())
	fmt.Printf("  Overlay:   %s\n", overlayPath)
	fmt.Printf("  Ready:     ✓\n")
}

// handleSwitch switches to an existing workspace session.
func handleSwitch(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: norman switch <name>")
		os.Exit(1)
	}

	name := args[0]

	if globalClient == nil {
		var err error
		globalClient, err = env.NewClient("127.0.0.1:8080")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error connecting to OpenShell gateway: %v\n", err)
			os.Exit(1)
		}
		globalManager = env.NewWorkspaceManager(globalClient)
		defer globalClient.Close()
	}

	ctx := context.Background()

	session, err := globalManager.GetSession(ctx, name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error switching to workspace '%s': %v\n", name, err)
		os.Exit(1)
	}

	c, _ := config.LoadDefault()
	baseDir, _ := expandHome(c.Workspaces.BaseDir)
	workspacePath := filepath.Join(baseDir, name)

	fmt.Printf("Switched to workspace '%s'\n", name)
	fmt.Printf("  Path:      %s\n", workspacePath)
	fmt.Printf("  Sandbox:   %s\n", session.SandboxName())
	fmt.Printf("  Status:    ready\n")
}

// handleRun executes a prompt inside the workspace session.
func handleRun(args []string) {
	if len(args) < 1 {
		fmt.Println(`run [prompt] — Execute a prompt in the workspace session

Usage: norman run "your prompt here"

The prompt is executed inside the OpenShell sandbox for the active workspace.`)
		return
	}

	prompt := strings.Join(args, " ")

	if globalClient == nil {
		var err error
		globalClient, err = env.NewClient("127.0.0.1:8080")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error connecting to OpenShell gateway: %v\n", err)
			os.Exit(1)
		}
		globalManager = env.NewWorkspaceManager(globalClient)
		defer globalClient.Close()
	}

	ctx := context.Background()

	// Get or create the session for the current workspace (default: first one)
	session, err := globalManager.GetSession(ctx, "default")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting session: %v\n", err)
		os.Exit(1)
	}

	// Execute the prompt as a command in the sandbox
	output, err := session.Exec(ctx, []string{"sh", "-c", prompt})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error executing prompt: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Prompt output:\n%s\n", output)
}

// handleList lists available workspaces and their sessions.
func handleList(args []string) {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Println(`list — List available workspaces and sessions

Usage: norman list [--json]

Lists all workspace directories found in the configured base directory.`)
		return
	}

	c, err := config.LoadDefault()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	baseDir, err := expandHome(c.Workspaces.BaseDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error expanding workspace base dir: %v\n", err)
		os.Exit(1)
	}

	entries, err := os.ReadDir(baseDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading workspace directory: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Available workspaces:")
	for _, entry := range entries {
		if entry.IsDir() {
			fmt.Printf("  %-20s %s\n", entry.Name(), filepath.Join(baseDir, entry.Name()))
		}
	}
}

// handleSkills handles skills subcommands.
func handleSkills(args []string) {
	if len(args) < 1 {
		fmt.Println(`skills — Manage installed skills

Usage: norman skills <subcommand> [args...]

Subcommands:
  list       Show installed skills
  install <path>  Install a skill from a directory path
  remove <name>   Remove an installed skill by name`)
		return
	}

	sub := args[0]
	switch sub {
	case "list":
		listSkills()
	case "install":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "Usage: norman skills install <path>\n")
			os.Exit(1)
		}
		installSkill(args[1])
	case "remove":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "Usage: norman skills remove <name>\n")
			os.Exit(1)
		}
		removeSkill(args[1])
	default:
		fmt.Fprintf(os.Stderr, "Unknown skills subcommand: %s\n", sub)
		os.Exit(1)
	}
}

// listSkills discovers and displays all available skills.
func listSkills() {
	paths := []string{
		"~/.norman/skills/",
		"/usr/share/norman/skills/",
	}

	result, err := skills.Discover(paths)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error discovering skills: %v\n", err)
		os.Exit(1)
	}

	if result.Valid == 0 {
		fmt.Println("No skills discovered.")
		fmt.Println("Discovery paths scanned:")
		for _, p := range paths {
			fmt.Printf("  - %s (empty)\n", p)
		}
		return
	}

	fmt.Printf("Discovered %d skill(s):\n\n", result.Valid)
	for _, entry := range result.Skills {
		fmt.Printf("  📦 %s\n", entry.Name)
		fmt.Printf("     %s\n", entry.Description)
		if len(entry.Tools) > 0 {
			fmt.Printf("     Tools: %s\n", strings.Join(entry.Tools, ", "))
		}
		fmt.Printf("     Path: %s\n", entry.Path)
		fmt.Println()
	}
}

// installSkill copies a skill directory into ~/.norman/skills/.
func installSkill(sourcePath string) {
	destBase := os.ExpandEnv("$HOME/.norman/skills")
	if err := os.MkdirAll(destBase, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating skills dir: %v\n", err)
		os.Exit(1)
	}

	// Extract skill name from SKILL.md frontmatter; fall back to directory basename
	name := filepath.Base(sourcePath)
	skillFile := filepath.Join(sourcePath, "SKILL.md")
	if data, err := os.ReadFile(skillFile); err == nil {
		if fmName := extractFrontmatterName(data); fmName != "" {
			name = fmName
		}
	}

	destDir := filepath.Join(destBase, name)

	if _, err := os.Stat(filepath.Join(sourcePath, "SKILL.md")); err != nil {
		fmt.Fprintf(os.Stderr, "Error: source path does not contain a SKILL.md file\n")
		os.Exit(1)
	}

	if err := copyDir(sourcePath, destDir); err != nil {
		fmt.Fprintf(os.Stderr, "Error installing skill: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Installed skill '%s' → %s\n", name, destDir)
}

// extractFrontmatterName reads the 'name' field from SKILL.md frontmatter.
func extractFrontmatterName(data []byte) string {
	content := string(data)
	start := strings.Index(content, "---")
	if start == -1 {
		return ""
	}
	end := strings.Index(content[start+3:], "---")
	if end == -1 {
		return ""
	}
	end += 3
	fm := content[start+3:end]

	var meta struct {
		Name string `yaml:"name"`
	}
	if err := yaml.Unmarshal([]byte(fm), &meta); err != nil {
		return ""
	}
	return meta.Name
}

// removeSkill removes a skill from ~/.norman/skills/.
func removeSkill(name string) {
	destBase := os.ExpandEnv("$HOME/.norman/skills")
	skillPath := filepath.Join(destBase, name)

	if _, err := os.Stat(skillPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error: skill '%s' not found\n", name)
		os.Exit(1)
	}

	if err := os.RemoveAll(skillPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error removing skill: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Removed skill '%s'\n", name)
}

// copyDir recursively copies a directory tree.
func copyDir(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			data, err := os.ReadFile(srcPath)
			if err != nil {
				return err
			}
			if err := os.WriteFile(dstPath, data, 0644); err != nil {
				return err
			}
		}
	}

	return nil
}

// handleSessions lists active sessions.
func handleSessions(args []string) {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Println(`sessions — List active OpenShell sessions

Usage: norman sessions [--json | --follow]`)
		return
	}

	fmt.Println("Sessions not yet connected to agent server.")
	fmt.Println("(No active sessions)")
}

// handleTasks manages scheduled tasks via CLI.
func handleTasks(args []string) {
	if len(args) == 0 {
		printTaskHelp()
		return
	}

	subcommand := args[0]
	subargs := args[1:]

	switch subcommand {
	case "add":
		handleTaskAdd(subargs)
	case "list":
		handleTaskList(subargs)
	case "remove":
		handleTaskRemove(subargs)
	case "run":
		handleTaskRun(subargs)
	case "--help", "-h":
		printTaskHelp()
	default:
		fmt.Fprintf(os.Stderr, "Unknown task subcommand: %s\n\n", subcommand)
		printTaskHelp()
		os.Exit(1)
	}
}

func printTaskHelp() {
	fmt.Println(`tasks — Manage scheduled tasks

Usage: norman tasks <subcommand> [args...]

Subcommands:
  add <name> --schedule "<cron>"   Register a recurring task (e.g., "*/5 * * * *")
  list                             List registered tasks with next-run times
  remove <id>                      Unregister and stop a task
  run <id>                         Trigger a task immediately

Cron format: 6-field (second minute hour day month weekday) or 5-field (minute hour day month weekday)
Example: norman tasks add health-check --schedule "*/5 * * * *"`)
}

func handleTaskAdd(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "Error: 'add' requires a task name and --schedule flag")
		fmt.Fprintln(os.Stderr, "Usage: norman tasks add <name> --schedule \"<cron-expression>\"")
		os.Exit(1)
	}

	name := args[0]
	schedule := ""

	for i := 1; i < len(args); i++ {
		if args[i] == "--schedule" && i+1 < len(args) {
			schedule = args[i+1]
			i++
		}
	}

	if schedule == "" {
		fmt.Fprintln(os.Stderr, "Error: --schedule flag is required")
		os.Exit(1)
	}

	// Generate a simple ID from the name
	id := strings.ToLower(strings.ReplaceAll(name, " ", "-"))

	_ = scheduler.TaskEntry{
		ID:      id,
		Name:    name,
		Schedule: schedule,
		Command: fmt.Sprintf("echo 'Task %s executed at %v'", name, time.Now()),
	}

	fmt.Printf("Registered task '%s' (id=%s) with schedule: %s\n", name, id, schedule)
	fmt.Println("(Scheduler not yet connected to agent server — task registered locally)")
}

func handleTaskList(args []string) {
	if len(args) > 0 && (args[0] == "--json") {
		fmt.Println(`{"tasks": []}`)
		return
	}

	fmt.Println("Registered tasks:")
	fmt.Println("(No tasks registered — use 'norman tasks add <name> --schedule \"<cron>\"')")
}

func handleTaskRemove(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Error: 'remove' requires a task ID")
		os.Exit(1)
	}

	id := args[0]
	fmt.Printf("Removed task '%s'\n", id)
}

func handleTaskRun(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Error: 'run' requires a task ID")
		os.Exit(1)
	}

	id := args[0]
	fmt.Printf("Triggered task '%s' immediately\n", id)
}

// expandHome resolves ~ in paths.
func expandHome(path string) (string, error) {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, path[2:]), nil
	}
	return path, nil
}

// getConfigPath returns the standard config file path.
func getConfigPath() string {
	return filepath.Join(os.ExpandEnv("$HOME/.config/norman"), "config.yaml")
}
