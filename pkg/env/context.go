// Package env provides workspace context injection for OpenShell sandboxes.
package env

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"
)

// ContextFile is the metadata file injected into each sandbox at creation time.
// It allows tools like `roger` to auto-detect workspace identity.
// Server URL is read from the NORMAN_SERVER_URL environment variable instead.
type ContextFile struct {
	WorkspaceID   string `json:"workspace_id"`
	WorkspaceName string `json:"workspace_name"`
}

// WriteContextFile injects a context.json into the sandbox's home directory.
func WriteContextFile(ctx context.Context, session *Session, ctxFile ContextFile) error {
	jsonData, err := json.MarshalIndent(ctxFile, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal context: %w", err)
	}

	// Write to ~/.config/norman/context.json inside the sandbox
	contentPath := filepath.Join("~", ".config", "norman", "context.json")

	// Ensure parent dirs exist (mkdir -p ~/.config/norman), retrying with backoff
	// since the sandbox may not be fully initialized yet.
	var mkdirErr error
	for i := 0; i < 5; i++ {
		mkdirCmd := fmt.Sprintf("mkdir -p %q", filepath.Join("~", ".config", "norman"))
		if _, err := session.Exec(ctx, []string{"bash", "-c", mkdirCmd}); err == nil {
			break
		}
		mkdirErr = err
		if i < 4 {
			time.Sleep(time.Duration(1<<uint(i)) * 200 * time.Millisecond)
		}
	}
	if mkdirErr != nil {
		return fmt.Errorf("mkdir context dir: %w", mkdirErr)
	}

	// Write the file, retrying with backoff
	for i := 0; i < 5; i++ {
		writeCmd := fmt.Sprintf("printf '%%s' '%s' > %q", escapeShellArg(string(jsonData)), contentPath)
		if _, err := session.Exec(ctx, []string{"bash", "-c", writeCmd}); err == nil {
			return nil
		}
		if i < 4 {
			time.Sleep(time.Duration(1<<uint(i)) * 200 * time.Millisecond)
		}
	}
	return fmt.Errorf("write context.json after retries")
}
