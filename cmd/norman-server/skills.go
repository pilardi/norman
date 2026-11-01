package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/norman/norman/pkg/protocol"
	"github.com/norman/norman/pkg/skills"
)

// UploadSkill writes uploaded skill files into the skills directory.
func (s *server) UploadSkill(w http.ResponseWriter, r *http.Request) {
	var req protocol.SkillUploadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid request body"})
		return
	}

	if len(req.Files) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "files array must not be empty"})
		return
	}

	destDir := filepath.Join(s.cfg.System.Skills.Path, req.Name)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{
			"error": fmt.Sprintf("failed to create skill dir: %v", err),
		})
		return
	}

	for _, f := range req.Files {
		dst := filepath.Join(destDir, f.Name)
		if err := os.WriteFile(dst, []byte(f.Content), 0644); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{
				"error": fmt.Sprintf("failed to write %s: %v", f.Name, err),
			})
			return
		}
	}

	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr(fmt.Sprintf("Installed skill '%s' with %d file(s)", req.Name, len(req.Files))),
		Step:    intPtr(0),
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"installed": true,
		"path":      destDir,
	})
}

// ListSkills discovers and returns all available skills.
func (s *server) ListSkills(w http.ResponseWriter, r *http.Request) {
	skillsResult, err := skills.Discover(s.cfg.System.Skills.DiscoveryPaths)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
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

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// InstallSkill clones a git repo into the skills directory and runs graphify install.
func (s *server) InstallSkill(w http.ResponseWriter, r *http.Request) {
	var req protocol.SkillInstallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid request body"})
		return
	}

	var skillName string
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
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{
			"error": fmt.Sprintf("git clone failed: %v", string(out)),
		})
		return
	}

	// Step 2: Checkout specific version/tag/branch if provided
	if req.Version != nil && *req.Version != "" {
		s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
			Event:   protocol.TaskProgress,
			Message: strPtr("Checking out version " + *req.Version),
		})

		cmd = exec.Command("git", "-C", destDir, "checkout", *req.Version)
		if out, err := cmd.CombinedOutput(); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{
				"error": fmt.Sprintf("git checkout failed: %v", string(out)),
			})
			return
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
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{
			"error": fmt.Sprintf("graphify install failed: %v", string(out)),
		})
		return
	}

	// Step 4: Register in discovery paths
	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr("Registered skill '" + skillName + "' in discovery"),
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"installed": true,
		"path":      destDir,
	})
}

// RemoveSkill uninstalls a skill by name.
func (s *server) RemoveSkill(w http.ResponseWriter, r *http.Request, name string) {
	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr("Removing skill: " + name),
	})

	if err := skills.Uninstall(name, s.cfg.System.Skills.Path); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.WriteHeader(http.StatusOK)
}

// UpdateSkill updates a skill from its source repo.
func (s *server) UpdateSkill(w http.ResponseWriter, r *http.Request, name string) {
	var req protocol.SkillInstallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid request body"})
		return
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
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{
			"error": err.Error(),
		})
		return
	}

	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr("Skill '" + entry.Name + "' updated to version " + entry.Version),
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"name":    entry.Name,
		"version": entry.Version,
		"path":    entry.Path,
		"updated": true,
	})
}

// CheckSkillVersion checks if a skill has an update available.
func (s *server) CheckSkillVersion(w http.ResponseWriter, r *http.Request, name string) {
	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr("Checking update availability for skill: " + name),
	})

	entry, err := skills.GetEntry(name, s.cfg.System.Skills.Path)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	hasUpdate, newVersion, err := skills.HasUpdate(*entry)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"name":              entry.Name,
		"installed_version": entry.Version,
		"has_update":        hasUpdate,
		"latest_version":    newVersion,
	})
}

// AddDiscoveryPaths adds new paths to the skill discovery configuration.
func (s *server) AddDiscoveryPaths(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Paths []string `json:"paths"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid request body"})
		return
	}

	if len(req.Paths) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "paths array must not be empty"})
		return
	}

	if err := s.loop.AddDiscoveryPaths(req.Paths); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr(fmt.Sprintf("Added %d discovery path(s) for skill scanning", len(req.Paths))),
		Step:    intPtr(0),
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"added": len(req.Paths),
		"paths": req.Paths,
	})
}

// GetSkillsRegistry returns the global skills registry.
func (s *server) GetSkillsRegistry(w http.ResponseWriter, r *http.Request) {
	entries := s.registry.GetAll()
	result := map[string]interface{}{}

	for name, entry := range entries {
		result[name] = map[string]interface{}{
			"source_url":              entry.SourceURL,
			"version":                 entry.Version,
			"description":             entry.Description,
			"installed_in_workspaces": entry.InstalledInWorkspaces,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// InstallSkillToWorkspace clones or uploads a skill into the target workspace's sandbox.
func (s *server) InstallSkillToWorkspace(w http.ResponseWriter, r *http.Request, ws string) {
	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr("Installing skill into workspace '" + ws + "'"),
	})

	// Accept either a repo URL (git clone path) or files[] (upload path)
	var req struct {
		Repo    string `json:"repo"`
		Version string `json:"version"`
		Name    string `json:"name"`
		Files   []struct {
			Name       string `json:"name"`
			Content    string `json:"content"`
			Executable bool   `json:"executable,omitempty"`
		} `json:"files"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid request body"})
		return
	}

	// Get the session for this workspace
	session, err := s.loop.GetSession(ws, "")
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{
			"error": fmt.Sprintf("get session for workspace '%s': %v", ws, err),
		})
		return
	}

	skillDir := filepath.Join("/workspace", ws, "skills", req.Name)

	// Path 1: Git clone from repo URL
	if req.Repo != "" {
		s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
			Event:   protocol.TaskProgress,
			Message: strPtr("Cloning " + req.Repo + " into sandbox"),
		})

		cmd := exec.Command("git", "clone", "--depth", "1", req.Repo, skillDir)
		if out, err := cmd.CombinedOutput(); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{
				"error": fmt.Sprintf("git clone: %v", string(out)),
			})
			return
		}

		if req.Version != "" {
			cmd = exec.Command("git", "-C", skillDir, "checkout", req.Version)
			if out, err := cmd.CombinedOutput(); err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]string{
					"error": fmt.Sprintf("git checkout: %v", string(out)),
				})
				return
			}
		}

		s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
			Event:   protocol.TaskProgress,
			Message: strPtr("Skill '" + req.Name + "' installed in workspace '" + ws + "'"),
		})

		// Deploy to sandbox and register in global registry
		if entry, err := s.activator.DeployToSandbox(req.Name, session); err != nil {
			log.Printf("[server] DeployToSandbox for '%s' in workspace '%s': %v", req.Name, ws, err)
		} else if entry != nil {
			s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
				Event:   protocol.TaskProgress,
				Message: strPtr("Deployed '" + entry.Name + "' to sandbox"),
			})

			// Register in global persistent registry
			_ = s.registry.Register(req.Name, skills.RegistryEntry{
				SourceURL:             entry.SourceURL,
				Version:               entry.Version,
				Description:           entry.Description,
				InstalledInWorkspaces: []string{ws},
			})

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"installed": true,
				"path":      skillDir,
				"type":      "git-clone",
			})
			return
		}

		// Fallback if DeployToSandbox failed
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"installed": true,
			"path":      skillDir,
			"type":      "git-clone",
		})
		return
	}

	// Path 2: Upload files (thin-client)
	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr("Uploading skill '" + req.Name + "' to workspace '" + ws + "'"),
	})

	// Create directory if needed
	cmd := exec.Command("mkdir", "-p", skillDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{
			"error": fmt.Sprintf("mkdir: %v", string(out)),
		})
		return
	}

	for _, f := range req.Files {
		content := []byte(f.Content)
		mode := os.FileMode(0644)
		if f.Executable {
			mode = 0755
		}
		fullPath := filepath.Join(skillDir, f.Name)
		if err := os.WriteFile(fullPath, content, mode); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{
				"error": fmt.Sprintf("write %s: %v", f.Name, err),
			})
			return
		}
	}

	s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
		Event:   protocol.TaskProgress,
		Message: strPtr("Skill '" + req.Name + "' uploaded to workspace '" + ws + "'"),
	})

	// Deploy to sandbox and register in global registry
	if entry, err := s.activator.DeployToSandbox(req.Name, session); err != nil {
		log.Printf("[server] DeployToSandbox for '%s' in workspace '%s': %v", req.Name, ws, err)
	} else if entry != nil {
		s.wsHub.BroadcastEvent(protocol.WebSocketEvent{
			Event:   protocol.TaskProgress,
			Message: strPtr("Deployed '" + entry.Name + "' to sandbox"),
		})

		// Register in global persistent registry
		_ = s.registry.Register(req.Name, skills.RegistryEntry{
			SourceURL:             entry.SourceURL,
			Version:               entry.Version,
			Description:           entry.Description,
			InstalledInWorkspaces: []string{ws},
		})

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"installed": true,
			"path":      skillDir,
			"type":      "upload",
		})
		return
	}

	// Fallback if DeployToSandbox failed (files present but SKILL.md unreadable)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"installed": true,
		"path":      skillDir,
		"type":      "upload",
	})
}
