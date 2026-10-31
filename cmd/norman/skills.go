package main

import (
	"context"
	"fmt"

	"github.com/norman/norman/pkg/protocol"
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
		if err := installSkill(ctx, cmd.Args().First()); err != nil {
			return fmt.Errorf("install failed: %v", err)
		}
		return nil
	case "remote", "rm":
		if len(cmd.Args().Slice()) < 1 {
			return fmt.Errorf("usage: norman skills remote <git-url> [version]")
		}
		if err := installRemote(ctx, cmd.Args().First()); err != nil {
			return fmt.Errorf("remote install failed: %v", err)
		}
		return nil
	case "remove":
		if len(cmd.Args().Slice()) < 1 {
			return fmt.Errorf("usage: norman skills remove <name>")
		}
		if err := removeSkill(ctx, cmd.Args().First()); err != nil {
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
	client, err := newProtocolClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %v", err)
	}

	rsp, err := client.ListSkills(ctx)
	if err != nil {
		return fmt.Errorf("error fetching skills: %v", err)
	}

	parsed, err := protocol.ParseListSkillsResponse(rsp)
	if err != nil {
		return fmt.Errorf("error parsing response: %v", err)
	}

	skills := parsed.GetJSON200()
	if skills == nil || len(*skills) == 0 {
		fmt.Println("No skills discovered.")
		return nil
	}

	fmt.Printf("Discovered %d skill(s):\n\n", len(*skills))
	for _, s := range *skills {
		name := strVal(s.Name)
		desc := strVal(s.Description)
		version := strVal(s.Version)

		fmt.Printf("  📦 %s\n", name)
		if desc != "" {
			fmt.Printf("     %s\n", desc)
		}
		if len(*s.Tools) > 0 {
			toolNames := make([]string, len(*s.Tools))
			for i, t := range *s.Tools {
				toolNames[i] = strVal(t.Name)
			}
			fmt.Printf("     Tools: %s\n", joinStrings(toolNames, ", "))
		}
		if version != "" {
			fmt.Printf("     Version: %s\n", version)
		}
		fmt.Println()
	}
	return nil
}

// installSkill delegates to the server's /api/skills/install endpoint.
func installSkill(ctx context.Context, sourcePath string) error {
	client, err := newProtocolClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %v", err)
	}

	body := protocol.SkillInstallRequest{
		Repo: sourcePath,
	}

	rsp, err := client.InstallSkill(ctx, body)
	if err != nil {
		return fmt.Errorf("error installing skill: %v", err)
	}

	_, err = protocol.ParseInstallSkillResponse(rsp)
	if err != nil {
		return fmt.Errorf("error parsing response: %v", err)
	}

	fmt.Printf("Requesting server to install skill from '%s'\n", sourcePath)
	return nil
}

// installRemote installs a skill from a git repository URL.
func installRemote(ctx context.Context, repoURL string) error {
	client, err := newProtocolClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %v", err)
	}

	body := protocol.SkillInstallRequest{
		Repo: repoURL,
	}

	rsp, err := client.InstallSkill(ctx, body)
	if err != nil {
		return fmt.Errorf("error installing skill: %v", err)
	}

	_, err = protocol.ParseInstallSkillResponse(rsp)
	if err != nil {
		return fmt.Errorf("error parsing response: %v", err)
	}

	fmt.Printf("Requesting server to install skill from repo '%s'\n", repoURL)
	return nil
}

// removeSkill delegates to the server's /api/skills/:name endpoint.
func removeSkill(ctx context.Context, name string) error {
	client, err := newProtocolClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %v", err)
	}

	rsp, err := client.RemoveSkill(ctx, name)
	if err != nil {
		return fmt.Errorf("error removing skill: %v", err)
	}

	_, err = protocol.ParseRemoveSkillResponse(rsp)
	if err != nil {
		return fmt.Errorf("error parsing response: %v", err)
	}

	fmt.Printf("Requesting server to remove skill '%s'\n", name)
	return nil
}

// joinStrings joins a slice of strings with the given separator.
func joinStrings(ss []string, sep string) string {
	if len(ss) == 0 {
		return ""
	}
	result := ss[0]
	for i := 1; i < len(ss); i++ {
		result += sep + ss[i]
	}
	return result
}
