package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// EnvVar represents a single environment variable that can override configuration.
type EnvVar struct {
	Name      string // e.g., "NORMAN_HOST"
	Type      string // "string", "int", "bool", "float64"
	Default   string // default value (as string)
	EnvKey    string // the actual env var key to read
	HelpText  string // description for help output
	Apply     func(sc *SystemConfig, raw string) error
}

// allEnvVars defines every supported NORMAN_* environment variable.
// Env vars take priority over .env file values and config.yaml settings.
var allEnvVars = []EnvVar{
	{
		Name: "NORMAN_HOST",
		Type: "string",
		Default: "127.0.0.1",
		HelpText: "Server bind address (default: \"127.0.0.1\")",
		Apply: func(sc *SystemConfig, raw string) error {
			sc.Host = raw
			return nil
		},
	},
	{
		Name: "NORMAN_PORT",
		Type: "int",
		Default: "8080",
		HelpText: "Server port number (default: 8080)",
		Apply: func(sc *SystemConfig, raw string) error {
			n, err := strconv.Atoi(raw)
			if err != nil {
				return fmt.Errorf("invalid NORMAN_PORT value %q: %w", raw, err)
			}
			sc.ServerPort = n
			return nil
		},
	},
	{
		Name: "NORMAN_SERVER_URL",
		Type: "string",
		Default: "",
		HelpText: "External-facing server URL (e.g., https://norman.example.com). Overrides auto-derived http://host:port URL.",
		Apply: func(sc *SystemConfig, raw string) error {
			sc.ServerURL = raw
			return nil
		},
	},
	{
		Name: "NORMAN_BASE_DIR",
		Type: "string",
		Default: "~/.norman/workspaces",
		HelpText: "Base directory for workspaces (default: ~/.norman/workspaces)",
		Apply: func(sc *SystemConfig, raw string) error {
			sc.BaseDir = raw
			return nil
		},
	},
	{
		Name: "NORMAN_VERBOSE",
		Type: "bool",
		Default: "false",
		HelpText: "Enable verbose/debug logging (true/false or 1/0)",
		Apply: func(sc *SystemConfig, raw string) error {
			sc.Logging.Verbose = (raw == "true" || raw == "1")
			return nil
		},
	},
	{
		Name: "NORMAN_STORE_DIR",
		Type: "string",
		Default: "$HOME/.norman/store",
		HelpText: "Path to the memory store directory (default: $HOME/.norman/store)",
		Apply: func(sc *SystemConfig, raw string) error {
			// This is consumed by the caller; we just return it for discovery.
			os.Setenv("NORMAN_STORE_DIR", raw)
			return nil
		},
	},
	{
		Name: "NORMAN_RAW_SOURCES_DIR",
		Type: "string",
		Default: "$HOME/.norman/raw_sources",
		HelpText: "Path to the raw sources store directory (default: $HOME/.norman/raw_sources)",
		Apply: func(sc *SystemConfig, raw string) error {
			os.Setenv("NORMAN_RAW_SOURCES_DIR", raw)
			return nil
		},
	},
}

// GetEnvVarNames returns the list of supported NORMAN_* environment variable names.
func GetEnvVarNames() []string {
	names := make([]string, len(allEnvVars))
	for i, ev := range allEnvVars {
		names[i] = ev.Name
	}
	return names
}

// GetEnvVarHelp returns the help text for a specific env var name.
func GetEnvVarHelp(name string) string {
	for _, ev := range allEnvVars {
		if ev.Name == name {
			return fmt.Sprintf("  %-24s %s", ev.Name, ev.HelpText)
		}
	}
	return ""
}

// loadEnvFile loads environment variables from a .env file.
// Returns an error only if the file exists but cannot be read.
func loadEnvFile(path string) error {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil // File doesn't exist is not an error
		}
		return fmt.Errorf("stat %s: %w", path, err)
	}
	return godotenv.Load(path)
}

// LoadEnvFiles loads .env files from standard locations.
// Priority order (later files override earlier ones):
//   1. Current directory .env
//   2. $HOME/.norman/.env
func LoadEnvFiles() error {
	// Load from current directory first (highest priority for local overrides)
	if err := loadEnvFile(".env"); err != nil {
		return fmt.Errorf("loading .env: %w", err)
	}

	// Then load from $HOME/.norman/.env
	home, err := os.UserHomeDir()
	if err == nil {
		normanDotEnv := filepath.Join(home, ".norman", ".env")
		if err := loadEnvFile(normanDotEnv); err != nil {
			return fmt.Errorf("loading %s: %w", normanDotEnv, err)
		}
	}

	return nil
}

// ApplyEnvOverrides reads all NORMAN_* environment variables and applies them
// to the given SystemConfig. This function should be called AFTER loading .env
// files and config.yaml, so env vars take final priority.
func ApplyEnvOverrides(sc *SystemConfig) error {
	var errs []string

	for _, ev := range allEnvVars {
		raw := os.Getenv(ev.Name)
		if raw == "" {
			continue // Not set; skip
		}

		if err := ev.Apply(sc, raw); err != nil {
			errs = append(errs, fmt.Sprintf("%s=%q: %v", ev.Name, raw, err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("env var override errors:\n  %s", strings.Join(errs, "\n  "))
	}

	return nil
}

// BuildEnvVarHelpText returns a formatted help string for all NORMAN_* env vars.
func BuildEnvVarHelpText() string {
	var lines []string
	for _, ev := range allEnvVars {
		lines = append(lines, fmt.Sprintf("  %-24s %s", ev.Name, ev.HelpText))
	}
	return strings.Join(lines, "\n")
}

// isNORMANPrefix checks if a string starts with "NORMAN_".
func isNORMANPrefix(s string) bool {
	return strings.HasPrefix(s, "NORMAN_")
}

// ServerEnvVars returns the subset of env vars relevant to the server binary.
// These are the variables that control server behavior (bind address, port, directories, etc.).
func ServerEnvVars() []EnvVar {
	return allEnvVars // Server gets all 7
}

// ClientEnvVars returns the subset of env vars relevant to the thin client CLI.
// The client primarily connects via NORMAN_SERVER_URL; HOST/PORT are fallbacks.
// Directory management (STORE_DIR, RAW_SOURCES_DIR) is a server concern.
func ClientEnvVars() []EnvVar {
	return []EnvVar{
		{
			Name: "NORMAN_SERVER_URL",
			Type: "string",
			Default: "",
			HelpText: "Server URL to connect to (e.g., https://norman.example.com). Primary client connection target.",
			Apply: func(sc *SystemConfig, raw string) error {
				sc.ServerURL = raw
				return nil
			},
		},
		{
			Name: "NORMAN_HOST",
			Type: "string",
			Default: "127.0.0.1",
			HelpText: "Fallback server bind address (used only when NORMAN_SERVER_URL is not set)",
			Apply: func(sc *SystemConfig, raw string) error {
				sc.Host = raw
				return nil
			},
		},
		{
			Name: "NORMAN_PORT",
			Type: "int",
			Default: "8080",
			HelpText: "Fallback server port (used only when NORMAN_SERVER_URL is not set)",
			Apply: func(sc *SystemConfig, raw string) error {
				n, err := strconv.Atoi(raw)
				if err != nil {
					return fmt.Errorf("invalid NORMAN_PORT value %q: %w", raw, err)
				}
				sc.ServerPort = n
				return nil
			},
		},
		{
			Name: "NORMAN_VERBOSE",
			Type: "bool",
			Default: "false",
			HelpText: "Enable verbose/debug logging (true/false or 1/0)",
			Apply: func(sc *SystemConfig, raw string) error {
				sc.Logging.Verbose = (raw == "true" || raw == "1")
				return nil
			},
		},
	}
}

// ClientEnvVarHelpText returns a formatted help string for client-relevant NORMAN_* env vars.
func ClientEnvVarHelpText() string {
	var lines []string
	for _, ev := range ClientEnvVars() {
		lines = append(lines, fmt.Sprintf("  %-24s %s", ev.Name, ev.HelpText))
	}
	return strings.Join(lines, "\n")
}
