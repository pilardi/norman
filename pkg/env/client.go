// Package env provides OpenShell session management for Norman workspaces.
package env

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	v1 "github.com/NVIDIA/OpenShell/sdk/go/openshell/v1"
)

const (
	defaultProvider = "default"
)

// ResolveGatewayAddress returns the OpenShell gateway address to connect to.
// Priority: OPENSHELL_GATEWAY_ADDRESS env var > metadata.json from ~/.config/openshell/gateways/openshell/metadata.json.
// Returns an empty string if no valid address can be resolved (caller should fail).
func ResolveGatewayAddress() string {
	// 1. Check explicit env var first
	if addr := os.Getenv("OPENSHELL_GATEWAY_ADDRESS"); addr != "" {
		return sanitizeAddress(addr)
	}

	// 2. Read metadata.json from the OpenShell config directory
	metadataPath := filepath.Join(os.ExpandEnv("$HOME"), ".config", "openshell", "gateways", "openshell", "metadata.json")
	data, err := os.ReadFile(metadataPath)
	if err != nil {
		log.Printf("[env] ERROR: OpenShell metadata.json not found at %s; cannot resolve gateway address", metadataPath)
		return ""
	}

	var meta map[string]interface{}
	if err := json.Unmarshal(data, &meta); err != nil {
		log.Printf("[env] ERROR: failed to parse OpenShell metadata.json: %v", err)
		return ""
	}

	if endpoint, ok := meta["gateway_endpoint"].(string); ok && endpoint != "" {
		return sanitizeAddress(endpoint)
	}

	log.Printf("[env] ERROR: no gateway_endpoint found in metadata.json")
	return ""
}

// sanitizeAddress strips protocol prefixes (e.g. "https://", "http://") from an address.
// OpenShell's gRPC client does not support HTTPS for localhost; only host:port is valid.
func sanitizeAddress(addr string) string {
	// Strip common protocol prefixes
	addr = strings.TrimPrefix(addr, "https://")
	addr = strings.TrimPrefix(addr, "http://")
	return addr
}

// Client wraps the OpenShell SDK client and manages sandbox lifecycle.
type Client struct {
	client  *v1.Client
	gateway string
}

// NewClient creates a new OpenShell client connected to the given gateway address.
func NewClient(gateway string) (*Client, error) {
	c, err := v1.NewClient(v1.Config{
		Address: gateway,
		Auth:    v1.NoAuth(),
	})
	if err != nil {
		return nil, fmt.Errorf("create OpenShell client: %w", err)
	}

	// Verify gateway health
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	health, err := c.Health().Check(ctx)
	if err != nil {
		return nil, fmt.Errorf("gateway health check failed: %w", err)
	}

	if !health.Healthy {
		log.Printf("[env] WARNING: OpenShell gateway reports unhealthy")
	}

	return &Client{client: c, gateway: gateway}, nil
}

// CreateSandbox creates a persistent sandbox (session) for the given workspace.
func (c *Client) CreateSandbox(ctx context.Context, workspace, name string) (*v1.Sandbox, error) {
	sandbox, err := c.client.CreateSandboxFromTemplate(
		ctx,
		defaultProvider,
		name,
		"", // empty template = use default
		nil,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("create sandbox for workspace %s: %w", name, err)
	}

	return sandbox, nil
}

// WaitReady waits for a sandbox to become ready, with timeout.
func (c *Client) WaitReady(ctx context.Context, provider, name string) (*v1.Sandbox, error) {
	sb, err := c.client.Sandboxes().WaitReady(ctx, provider, name)
	if err != nil {
		return nil, fmt.Errorf("wait for sandbox %s/%s ready: %w", provider, name, err)
	}
	return sb, nil
}

// GetSandbox fetches the current state of a sandbox.
func (c *Client) GetSandbox(ctx context.Context, provider, name string) (*v1.Sandbox, error) {
	sb, err := c.client.Sandboxes().Get(ctx, provider, name)
	if err != nil {
		return nil, fmt.Errorf("get sandbox %s/%s: %w", provider, name, err)
	}
	return sb, nil
}

// DeleteSandbox removes a sandbox.
func (c *Client) DeleteSandbox(ctx context.Context, provider, name string) (*v1.DeletionResult, error) {
	result, err := c.client.Sandboxes().Delete(ctx, provider, name)
	if err != nil {
		return nil, fmt.Errorf("delete sandbox %s/%s: %w", provider, name, err)
	}
	return result, nil
}

// RunCommand executes a command inside a sandbox and returns stdout/stderr.
func (c *Client) RunCommand(ctx context.Context, provider, sandbox string, cmd []string) (*v1.ExecResult, error) {
	result, err := c.client.Exec().Run(ctx, provider, sandbox, cmd)
	if err != nil {
		return nil, fmt.Errorf("exec in sandbox %s/%s: %w", provider, sandbox, err)
	}
	return result, nil
}

// Close shuts down the OpenShell client connection.
func (c *Client) Close() error {
	return c.client.Close()
}
