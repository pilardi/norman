package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// handleSkills handles skills subcommands.
func handleSkills(args []string) {
	if len(args) < 1 {
		fmt.Println(`skills — Manage installed skills

Usage: norman skills <subcommand> [args...]

Subcommands:
  list       Show installed skills with provenance
  install <path|repo>  Install a skill from a local path or git repo URL
  remote <git-url>     Install a skill from a git repository (alias for install)
  remove <name>   Remove an installed skill by name`)
		return
	}

	sub := args[0]
	switch sub {
	case "list":
		listSkills()
	case "install":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "Usage: norman skills install <path|repo>\n")
			os.Exit(1)
		}
		installSkill(args[1])
	case "remote":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "Usage: norman skills remote <git-url> [version]\n")
			os.Exit(1)
		}
		installRemote(args[1])
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

// listSkills fetches installed skills from the server and displays provenance.
func listSkills() {
	body, err := httpGet("/api/skills")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error fetching skills: %v\n", err)
		os.Exit(1)
	}

	var skills []map[string]interface{}
	if err := json.Unmarshal(body, &skills); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing skills response: %v\n", err)
		os.Exit(1)
	}

	if len(skills) == 0 {
		fmt.Println("No skills discovered.")
		return
	}

	fmt.Printf("Discovered %d skill(s):\n\n", len(skills))
	for _, s := range skills {
		name, _ := s["name"].(string)
		desc, _ := s["description"].(string)
		path, _ := s["path"].(string)
		sourceURL, _ := s["source_url"].(string)
		version, _ := s["version"].(string)
		tools, _ := s["tools"].([]interface{})

		fmt.Printf("  📦 %s\n", name)
		fmt.Printf("     %s\n", desc)
		if len(tools) > 0 {
			toolNames := make([]string, len(tools))
			for i, t := range tools {
				if m, ok := t.(map[string]interface{}); ok {
					if n, ok := m["name"].(string); ok {
						toolNames[i] = n
					}
				}
			}
			fmt.Printf("     Tools: %s\n", strings.Join(toolNames, ", "))
		}
		fmt.Printf("     Path:    %s\n", path)
		if sourceURL != "" {
			fmt.Printf("     Source:  %s\n", sourceURL)
		}
		if version != "" {
			fmt.Printf("     Version: %s\n", version)
		}
		fmt.Println()
	}
}

// installSkill delegates to the server's /api/skills/install endpoint.
func installSkill(sourcePath string) {
	payload := map[string]string{"repo": sourcePath}
	if err := httpPost("/api/skills/install", payload); err != nil {
		fmt.Fprintf(os.Stderr, "Error installing skill: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Requesting server to install skill from '%s'\n", sourcePath)
}

// installRemote installs a skill from a git repository URL.
func installRemote(repoURL string) {
	payload := map[string]string{"repo": repoURL}
	if err := httpPost("/api/skills/install", payload); err != nil {
		fmt.Fprintf(os.Stderr, "Error installing skill: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Requesting server to install skill from repo '%s'\n", repoURL)
}

// removeSkill delegates to the server's /api/skills/:name endpoint.
func removeSkill(name string) {
	if err := httpDelete("/api/skills/" + name); err != nil {
		fmt.Fprintf(os.Stderr, "Error removing skill: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Requesting server to remove skill '%s'\n", name)
}
