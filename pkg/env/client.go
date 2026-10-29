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
	v1types "github.com/NVIDIA/OpenShell/sdk/go/openshell/v1/types"
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
		return addr
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
		return endpoint
	}

	log.Printf("[env] ERROR: no gateway_endpoint found in metadata.json")
	return ""
}

// ResolveMTLSConfig returns TLS certificate paths from the mtls/ directory
// alongside metadata.json, when auth_mode is "mtls". Returns nil if not applicable.
func ResolveMTLSConfig(gatewayDir string) *v1.TLSConfig {
	mtlsPath := filepath.Join(gatewayDir, "mtls")
	caFile := filepath.Join(mtlsPath, "ca.crt")
	certFile := filepath.Join(mtlsPath, "tls.crt")
	keyFile := filepath.Join(mtlsPath, "tls.key")

	// Check if mtls directory and required files exist
	if _, err := os.Stat(mtlsPath); os.IsNotExist(err) {
		return nil
	}
	if _, err := os.Stat(caFile); os.IsNotExist(err) {
		log.Printf("[env] WARNING: ca.crt not found in %s; mTLS disabled", mtlsPath)
		return nil
	}
	if _, err := os.Stat(certFile); os.IsNotExist(err) {
		log.Printf("[env] WARNING: tls.crt not found in %s; mTLS disabled", mtlsPath)
		return nil
	}
	if _, err := os.Stat(keyFile); os.IsNotExist(err) {
		log.Printf("[env] WARNING: tls.key not found in %s; mTLS disabled", mtlsPath)
		return nil
	}

	return &v1.TLSConfig{
		CertFile: certFile,
		KeyFile:  keyFile,
		CAFile:   caFile,
		Insecure: false,
	}
}

// Client wraps the OpenShell SDK client and manages sandbox lifecycle.
type Client struct {
	client  *v1.Client
	gateway string
	tlsCfg  *v1.TLSConfig // optional mTLS config for this connection
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

	return &Client{client: c, gateway: gateway, tlsCfg: nil}, nil
}

// NewClientWithTLS creates a new OpenShell client with optional mTLS configuration.
func NewClientWithTLS(gateway string, tlsCfg *v1.TLSConfig) (*Client, error) {
	c, err := v1.NewClient(v1.Config{
		Address: gateway,
		TLS:     tlsCfg,
		Auth:    v1.NoAuth(),
	})
	if err != nil {
		return nil, fmt.Errorf("create OpenShell client with TLS: %w", err)
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

	return &Client{client: c, gateway: gateway, tlsCfg: tlsCfg}, nil
}

// GetMTLSPaths returns the mTLS certificate and key paths configured on this client,
// or empty strings if mTLS is not configured.
func (c *Client) GetMTLSPaths() (certPath, keyPath string) {
	if c.tlsCfg == nil {
		return "", ""
	}
	return c.tlsCfg.CertFile, c.tlsCfg.KeyFile
}

// CreateSandbox creates a persistent sandbox (session) for the given workspace.
// If ctxFile is non-nil, a context.json file is injected into the sandbox.
func (c *Client) CreateSandbox(ctx context.Context, workspace, name string, ctxFile *ContextFile, sessionId string) (*v1.Sandbox, error) {
	// Try template-based creation first
	sandbox, err := c.client.CreateSandboxFromTemplate(
		ctx,
		defaultProvider,
		name,
		"default", // template name
		nil,
		nil,
	)
	if err == nil {
		return sandbox, nil
	}

	// Fall back to inline spec creation if template not found
	sandbox, err = c.client.Sandboxes().Create(
		ctx,
		defaultProvider,
		name,
		&v1.SandboxSpec{}, // empty inline spec (uses default image)
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("create sandbox for workspace %s: %w", name, err)
	}

	// Inject context file if provided
	if ctxFile != nil {
		session := NewSession(c, defaultProvider, sandbox.Name, sessionId)
		if injectErr := WriteContextFile(ctx, session, *ctxFile); injectErr != nil {
			log.Printf("[env] WARNING: failed to inject context into workspace %s: %v", name, injectErr)
		}
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

// WaitReadyWithTimeout waits for a sandbox to become ready with an explicit timeout.
func (c *Client) WaitReadyWithTimeout(ctx context.Context, provider, name string, timeout time.Duration) (*v1.Sandbox, error) {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	sb, err := c.client.Sandboxes().WaitReady(waitCtx, provider, name)
	if err != nil {
		return nil, fmt.Errorf("wait for sandbox %s/%s ready (timeout %v): %w", provider, name, timeout, err)
	}
	return sb, nil
}

// DeleteSandbox removes a sandbox and returns only the error.
func (c *Client) DeleteSandbox(ctx context.Context, provider, name string) error {
	_, err := c.client.Sandboxes().Delete(ctx, provider, name)
	if err != nil {
		return fmt.Errorf("delete sandbox %s/%s: %w", provider, name, err)
	}
	return nil
}

// StopSandbox stops a running sandbox (preserves it for later start).
func (c *Client) StopSandbox(ctx context.Context, provider, name string) error {
	_, err := c.client.Sandboxes().Stop(ctx, provider, name)
	if err != nil {
		return fmt.Errorf("stop sandbox %s/%s: %w", provider, name, err)
	}
	return nil
}

// StartSandbox starts a stopped sandbox. Returns error if sandbox doesn't exist.
func (c *Client) StartSandbox(ctx context.Context, provider, name string) error {
	_, err := c.client.Sandboxes().Start(ctx, provider, name)
	if err != nil {
		return fmt.Errorf("start sandbox %s/%s: %w", provider, name, err)
	}
	return nil
}

// RunCommandStr executes a command string inside a sandbox and returns (output, exitCode, error).
func (c *Client) RunCommandStr(ctx context.Context, provider, sandboxName string, command string) (string, int, error) {
	cmd := strings.Fields(command)
	result, err := c.client.Exec().Run(ctx, provider, sandboxName, cmd)
	if err != nil {
		return "", 1, fmt.Errorf("exec in sandbox %s/%s: %w", provider, sandboxName, err)
	}
	exitCode := result.ExitCode
	output := string(result.Stdout)
	return output, exitCode, nil
}

// GetSandbox fetches the current state of a sandbox.
func (c *Client) GetSandbox(ctx context.Context, provider, name string) (*v1.Sandbox, error) {
	sb, err := c.client.Sandboxes().Get(ctx, provider, name)
	if err != nil {
		return nil, fmt.Errorf("get sandbox %s/%s: %w", provider, name, err)
	}
	return sb, nil
}

// RunCommand executes a command inside a sandbox and returns stdout/stderr.
func (c *Client) RunCommand(ctx context.Context, provider, sandbox string, cmd []string) (*v1.ExecResult, error) {
	result, err := c.client.Exec().Run(ctx, provider, sandbox, cmd)
	if err != nil {
		return nil, fmt.Errorf("exec in sandbox %s/%s: %w", provider, sandbox, err)
	}
	return result, nil
}

// RunCommandWithOptions executes a command with the given options (e.g. environment variables).
func (c *Client) RunCommandWithOptions(ctx context.Context, provider, sandbox string, cmd []string, opts v1types.ExecOptions) (*v1.ExecResult, error) {
	result, err := c.client.Exec().Run(ctx, provider, sandbox, cmd, opts)
	if err != nil {
		return nil, fmt.Errorf("exec in sandbox %s/%s: %w", provider, sandbox, err)
	}
	return result, nil
}

// Close shuts down the OpenShell client connection.
func (c *Client) Close() error {
	return c.client.Close()
}
