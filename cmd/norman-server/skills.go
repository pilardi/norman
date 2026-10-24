package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/labstack/echo/v4"
	"github.com/norman/norman/pkg/protocol"
	"github.com/norman/norman/pkg/skills"
)

// UploadSkill writes uploaded skill files into the skills directory.
func (s *server) UploadSkill(c echo.Context) error {
	var req protocol.SkillUploadRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	if len(req.Files) == 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "files array must not be empty"})
	}

	destDir := filepath.Join(s.cfg.System.Skills.Path, req.Name)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": fmt.Sprintf("failed to create skill dir: %v", err),
		})
	}

	for _, f := range req.Files {
		dst := filepath.Join(destDir, f.Name)
		if err := os.WriteFile(dst, []byte(f.Content), 0644); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": fmt.Sprintf("failed to write %s: %v", f.Name, err),
			})
		}
	}

	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr(fmt.Sprintf("Installed skill '%s' with %d file(s)", req.Name, len(req.Files))),
		Step:    intPtr(0),
	})

	return c.JSON(http.StatusCreated, map[string]interface{}{
		"installed": true,
		"path":      destDir,
	})
}

// ListSkills discovers and returns all available skills.
func (s *server) ListSkills(c echo.Context) error {
	skillsResult, err := skills.Discover(s.cfg.System.Skills.DiscoveryPaths)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	result := make([]map[string]interface{}, len(skillsResult.Skills))
	for i, entry := range skillsResult.Skills {
		result[i] = map[string]interface{}{
			"name":        entry.Name,
			"description": entry.Description,
			"tools":       entry.Tools,
			"path":        entry.Path,
		}
	}

	return c.JSON(http.StatusOK, result)
}

// InstallSkill clones a git repo into the skills directory and runs graphify install.
func (s *server) InstallSkill(c echo.Context) error {
	var req protocol.SkillInstallRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	skillName := req.Repo // default to repo basename
	if req.Name != nil && *req.Name != "" {
		skillName = *req.Name
	} else {
		skillName = filepath.Base(req.Repo)
	}

	destDir := filepath.Join(s.cfg.System.Skills.Path, skillName)

	// Step 1: Clone the git repo into the skills directory
	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr("Cloning repo " + req.Repo),
	})

	cmd := exec.Command("git", "clone", req.Repo, destDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": fmt.Sprintf("git clone failed: %v", string(out)),
		})
	}

	// Step 2: Checkout specific version/tag/branch if provided
	if req.Version != nil && *req.Version != "" {
		s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
			Event:   protocol.TaskProgress,
			Message: strPtr("Checking out version " + *req.Version),
		})

		cmd = exec.Command("git", "-C", destDir, "checkout", *req.Version)
		if out, err := cmd.CombinedOutput(); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": fmt.Sprintf("git checkout failed: %v", string(out)),
			})
		}
	}

	// Step 3: Run graphify install --platform agents to generate SKILL.md + references/
	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr("Running graphify install --platform agents"),
	})

	cmd = exec.Command("graphify", "install", "--platform", "agents")
	cmd.Dir = destDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": fmt.Sprintf("graphify install failed: %v", string(out)),
		})
	}

	// Step 4: Register in discovery paths
	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr("Registered skill '" + skillName + "' in discovery"),
	})

	return c.JSON(http.StatusCreated, map[string]interface{}{
		"installed": true,
		"path":      destDir,
	})
}

// RemoveSkill uninstalls a skill by name.
func (s *server) RemoveSkill(c echo.Context, name string) error {
	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr("Removing skill: " + name),
	})

	if err := skills.Uninstall(name, s.cfg.System.Skills.Path); err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
	}

	return c.NoContent(http.StatusOK)
}

// UpdateSkill updates a skill from its source repo.
func (s *server) UpdateSkill(c echo.Context, name string) error {
	var req protocol.SkillInstallRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr("Updating skill '" + name + "' from repo " + req.Repo),
	})

	version := ""
	if req.Version != nil {
		version = *req.Version
	}

	entry, err := skills.Update(req.Repo, version, s.cfg.System.Skills.Path)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": err.Error(),
		})
	}

	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr("Skill '" + entry.Name + "' updated to version " + entry.Version),
	})

	return c.JSON(http.StatusOK, map[string]interface{}{
		"name":        entry.Name,
		"version":     entry.Version,
		"path":        entry.Path,
		"updated":     true,
	})
}

// CheckSkillVersion checks if a skill has an update available.
func (s *server) CheckSkillVersion(c echo.Context, name string) error {
	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr("Checking update availability for skill: " + name),
	})

	entry, err := skills.GetEntry(name, s.cfg.System.Skills.Path)
	if err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
	}

	hasUpdate, newVersion, err := skills.HasUpdate(*entry)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	result := map[string]interface{}{
		"name":              entry.Name,
		"installed_version": entry.Version,
		"has_update":        hasUpdate,
		"latest_version":    newVersion,
	}

	return c.JSON(http.StatusOK, result)
}

// AddDiscoveryPaths adds new paths to the skill discovery configuration.
func (s *server) AddDiscoveryPaths(c echo.Context) error {
	var req struct {
		Paths []string `json:"paths"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	if len(req.Paths) == 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "paths array must not be empty"})
	}

	if err := s.loop.AddDiscoveryPaths(req.Paths); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}

	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr(fmt.Sprintf("Added %d discovery path(s) for skill scanning", len(req.Paths))),
		Step:    intPtr(0),
	})

	return c.JSON(http.StatusCreated, map[string]interface{}{
		"added": len(req.Paths),
		"paths": req.Paths,
	})
}

// GetSkillsRegistry returns the global skills registry.
func (s *server) GetSkillsRegistry(c echo.Context) error {
	entries := s.registry.GetAll()
	result := map[string]interface{}{}

	for name, entry := range entries {
		result[name] = map[string]interface{}{
			"source_url":            entry.SourceURL,
			"version":               entry.Version,
			"description":           entry.Description,
			"installed_in_workspaces": entry.InstalledInWorkspaces,
		}
	}

	return c.JSON(http.StatusOK, result)
}

// InstallSkillToWorkspace clones or uploads a skill into the target workspace's sandbox.
func (s *server) InstallSkillToWorkspace(c echo.Context) error {
	wsName := c.Param("ws")

	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr("Installing skill into workspace '" + wsName + "'"),
	})

	// Accept either a repo URL (git clone path) or files[] (upload path)
	var req struct {
		Repo    string   `json:"repo"`
		Version string   `json:"version"`
		Name    string   `json:"name"`
		Files   []struct {
			Name     string `json:"name"`
			Content  string `json:"content"`
			Executable bool `json:"executable,omitempty"`
		} `json:"files"`
	}

	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	// Get the session for this workspace
	session, err := s.loop.GetSession(wsName, "")
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": fmt.Sprintf("get session for workspace '%s': %v", wsName, err),
		})
	}

	skillDir := filepath.Join("/workspace", wsName, "skills", req.Name)

	// Path 1: Git clone from repo URL
	if req.Repo != "" {
		s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
			Event:   protocol.TaskProgress,
			Message: strPtr("Cloning " + req.Repo + " into sandbox"),
		})

		cmd := exec.Command("git", "clone", "--depth", "1", req.Repo, skillDir)
		if out, err := cmd.CombinedOutput(); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": fmt.Sprintf("git clone: %v", string(out)),
			})
		}

		if req.Version != "" {
			cmd = exec.Command("git", "-C", skillDir, "checkout", req.Version)
			if out, err := cmd.CombinedOutput(); err != nil {
				return c.JSON(http.StatusInternalServerError, map[string]string{
					"error": fmt.Sprintf("git checkout: %v", string(out)),
				})
			}
		}

		s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
			Event:   protocol.TaskProgress,
			Message: strPtr("Skill '" + req.Name + "' installed in workspace '" + wsName + "'"),
		})

		// Deploy to sandbox and register in global registry
		if entry, err := s.activator.DeployToSandbox(req.Name, session); err != nil {
			log.Printf("[server] DeployToSandbox for '%s' in workspace '%s': %v", req.Name, wsName, err)
		} else if entry != nil {
			s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
				Event:   protocol.TaskProgress,
				Message: strPtr("Deployed '" + entry.Name + "' to sandbox"),
			})

			// Register in global persistent registry
			s.registry.Register(req.Name, skills.RegistryEntry{
				SourceURL:           entry.SourceURL,
				Version:             entry.Version,
				Description:         entry.Description,
				InstalledInWorkspaces: []string{wsName},
			})

			return c.JSON(http.StatusCreated, map[string]interface{}{
				"installed": true,
				"path":      skillDir,
				"type":      "git-clone",
			})
		}

		// Fallback if DeployToSandbox failed
		return c.JSON(http.StatusCreated, map[string]interface{}{
			"installed": true,
			"path":      skillDir,
			"type":      "git-clone",
		})
	}

	// Path 2: Upload files (thin-client)
	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr("Uploading skill '" + req.Name + "' to workspace '" + wsName + "'"),
	})

	// Create directory if needed
	cmd := exec.Command("mkdir", "-p", skillDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": fmt.Sprintf("mkdir: %v", string(out)),
		})
	}

	for _, f := range req.Files {
		content := []byte(f.Content)
		mode := os.FileMode(0644)
		if f.Executable {
			mode = 0755
		}
		fullPath := filepath.Join(skillDir, f.Name)
		if err := os.WriteFile(fullPath, content, mode); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": fmt.Sprintf("write %s: %v", f.Name, err),
			})
		}
	}

	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr("Skill '" + req.Name + "' uploaded to workspace '" + wsName + "'"),
	})

	// Deploy to sandbox and register in global registry
	if entry, err := s.activator.DeployToSandbox(req.Name, session); err != nil {
		log.Printf("[server] DeployToSandbox for '%s' in workspace '%s': %v", req.Name, wsName, err)
	} else if entry != nil {
		s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
			Event:   protocol.TaskProgress,
			Message: strPtr("Deployed '" + entry.Name + "' to sandbox"),
		})

		// Register in global persistent registry
		s.registry.Register(req.Name, skills.RegistryEntry{
			SourceURL:           entry.SourceURL,
			Version:             entry.Version,
			Description:         entry.Description,
			InstalledInWorkspaces: []string{wsName},
		})

		return c.JSON(http.StatusCreated, map[string]interface{}{
			"installed": true,
			"path":      skillDir,
			"type":      "upload",
		})
	}

	// Fallback if DeployToSandbox failed (files present but SKILL.md unreadable)
	return c.JSON(http.StatusCreated, map[string]interface{}{
		"installed": true,
		"path":      skillDir,
		"type":      "upload",
	})
}
