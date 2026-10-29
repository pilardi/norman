package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/urfave/cli/v3"
)

// handleSkills handles skills subcommands.
func handleSkills(ctx context.Context, cmd *cli.Command) error {
	if len(cmd.Args().Slice()) == 0 {
		return printSkillsHelp()
	}

	sub := cmd.Args().First()
	switch sub {
	case "list", "ls":
		return listSkills(ctx, cmd)
	case "install":
		if len(cmd.Args().Slice()) < 1 {
			return fmt.Errorf("usage: norman skills install <path|repo>")
		}
		if err := installSkill(cmd.Args().First()); err != nil {
			return fmt.Errorf("install failed: %v", err)
		}
		return nil
	case "remote", "rm":
		if len(cmd.Args().Slice()) < 1 {
			return fmt.Errorf("usage: norman skills remote <git-url> [version]")
		}
		if err := installRemote(cmd.Args().First()); err != nil {
			return fmt.Errorf("remote install failed: %v", err)
		}
		return nil
	case "remove":
		if len(cmd.Args().Slice()) < 1 {
			return fmt.Errorf("usage: norman skills remove <name>")
		}
		if err := removeSkill(cmd.Args().First()); err != nil {
			return fmt.Errorf("remove failed: %v", err)
		}
		return nil
	default:
		return fmt.Errorf("unknown skills subcommand: %s\n\n%s", sub, skillsHelpText)
	}
}

// printSkillsHelp displays help for the skills subcommand.
func printSkillsHelp() error {
	fmt.Println(skillsHelpText)
	return nil
}

const skillsHelpText = `skills — Manage installed skills

Usage: norman skills <subcommand> [args...]

Subcommands:
  list       Show installed skills with provenance
  install <path|repo>  Install a skill from a local path or git repo URL
  remote <git-url>     Install a skill from a git repository (alias for install)
  remove <name>   Remove an installed skill by name`

// listSkills fetches installed skills from the server and displays provenance.
func listSkills(ctx context.Context, cmd *cli.Command) error {
	body, err := httpGet("/api/skills")
	if err != nil {
		return fmt.Errorf("error fetching skills: %v", err)
	}

	var skills []map[string]interface{}
	if err := json.Unmarshal(body, &skills); err != nil {
		return fmt.Errorf("error parsing skills response: %v", err)
	}

	if len(skills) == 0 {
		fmt.Println("No skills discovered.")
		return nil
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
	return nil
}

// installSkill delegates to the server's /api/skills/install endpoint.
func installSkill(sourcePath string) error {
	payload := map[string]string{"repo": sourcePath}
	if err := httpPost("/api/skills/install", payload); err != nil {
		return fmt.Errorf("error installing skill: %v", err)
	}
	fmt.Printf("Requesting server to install skill from '%s'\n", sourcePath)
	return nil
}

// installRemote installs a skill from a git repository URL.
func installRemote(repoURL string) error {
	payload := map[string]string{"repo": repoURL}
	if err := httpPost("/api/skills/install", payload); err != nil {
		return fmt.Errorf("error installing skill: %v", err)
	}
	fmt.Printf("Requesting server to install skill from repo '%s'\n", repoURL)
	return nil
}

// removeSkill delegates to the server's /api/skills/:name endpoint.
func removeSkill(name string) error {
	if err := httpDelete("/api/skills/" + name); err != nil {
		return fmt.Errorf("error removing skill: %v", err)
	}
	fmt.Printf("Requesting server to remove skill '%s'\n", name)
	return nil
}
