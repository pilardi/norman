// Package env provides workspace context injection for OpenShell sandboxes.
package env

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
)

// ContextFile is the metadata file injected into each sandbox at creation time.
// It allows tools like `roger` to auto-detect workspace identity and mTLS paths.
// Server URL is read from the NORMAN_SERVER_URL environment variable instead.
type ContextFile struct {
	WorkspaceID    string `json:"workspace_id"`
	WorkspaceName  string `json:"workspace_name"`
	MTLSCertPath   string `json:"mtls_cert_path,omitempty"`
	MTLSKeyPath    string `json:"mtls_key_path,omitempty"`
}

// WriteContextFile injects a context.json into the sandbox's home directory.
func WriteContextFile(ctx context.Context, session *Session, ctxFile ContextFile) error {
	jsonData, err := json.MarshalIndent(ctxFile, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal context: %w", err)
	}

	// Write to ~/.config/norman/context.json inside the sandbox
	contentPath := filepath.Join("~", ".config", "norman", "context.json")

	// Ensure parent dirs exist (mkdir -p ~/.config/norman)
	mkdirCmd := fmt.Sprintf("mkdir -p %q", filepath.Join("~", ".config", "norman"))
	if _, err := session.Exec(ctx, []string{"bash", "-c", mkdirCmd}); err != nil {
		return fmt.Errorf("mkdir context dir: %w", err)
	}

	// Write the file
	writeCmd := fmt.Sprintf("printf '%%s' '%s' > %q", escapeShellArg(string(jsonData)), contentPath)
	if _, err := session.Exec(ctx, []string{"bash", "-c", writeCmd}); err != nil {
		return fmt.Errorf("write context.json: %w", err)
	}

	return nil
}


