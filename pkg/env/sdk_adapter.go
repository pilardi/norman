// Package env provides an adapter that wraps the real OpenShell SDK client
// and adapts its method signatures to match the OpenShellClient interface
// used by the agent loop.
package env

import (
	"context"
	"fmt"
	"strings"
	"time"

	v1 "github.com/NVIDIA/OpenShell/sdk/go/openshell/v1"
)

// SDKAdapter wraps *Client and adapts its methods to match the
// OpenShellClient interface defined in pkg/agent/loop.go.
type SDKAdapter struct {
	client *Client
}

// NewSDKAdapter creates an adapter from a real env.Client.
func NewSDKAdapter(c *Client) *SDKAdapter {
	return &SDKAdapter{client: c}
}

// CreateSandbox adapts env.Client.CreateSandbox to return the full sandbox object.
func (a *SDKAdapter) CreateSandbox(ctx context.Context, workspace, name string, ctxFile *ContextFile, sessionUUID string) (*v1.Sandbox, error) {
	sb, err := a.client.CreateSandbox(ctx, workspace, name, ctxFile, sessionUUID)
	if err != nil {
		return nil, fmt.Errorf("create sandbox: %w", err)
	}
	if sb == nil {
		return nil, fmt.Errorf("create sandbox: returned nil sandbox")
	}
	return sb, nil
}

// WaitReady adapts env.Client.WaitReady to match the interface signature.
func (a *SDKAdapter) WaitReady(ctx context.Context, workspace, name string, timeout time.Duration) (*v1.Sandbox, error) {
	sb, err := a.client.WaitReady(ctx, "default", name)
	if err != nil {
		return nil, fmt.Errorf("wait ready: %w", err)
	}
	return sb, nil
}

// RunCommand adapts env.Client.RunCommand to accept a single command string
// and return (output, exitCode, error).
func (a *SDKAdapter) RunCommand(ctx context.Context, sandboxName, command string) (string, int, error) {
	cmd := strings.Fields(command)
	result, err := a.client.RunCommand(ctx, "default", sandboxName, cmd)
	if err != nil {
		return "", 1, fmt.Errorf("run command: %w", err)
	}
	exitCode := result.ExitCode
	output := string(result.Stdout)
	return output, exitCode, nil
}

// GetSandbox adapts env.Client.GetSandbox to return the sandbox object.
func (a *SDKAdapter) GetSandbox(ctx context.Context, provider, name string) (*v1.Sandbox, error) {
	sb, err := a.client.GetSandbox(ctx, provider, name)
	if err != nil {
		return nil, fmt.Errorf("get sandbox: %w", err)
	}
	return sb, nil
}

// GetLogs adapts env.Client.GetSandbox to return log lines as []string.
func (a *SDKAdapter) GetLogs(ctx context.Context, sandboxID string) ([]string, error) {
	sb, err := a.client.GetSandbox(ctx, "default", sandboxID)
	if err != nil {
		return nil, fmt.Errorf("get logs: %w", err)
	}
	if sb == nil {
		return []string{}, nil
	}
	// Extract log lines from sandbox conditions
	var lines []string
	for _, cond := range sb.Status.Conditions {
		lines = append(lines, fmt.Sprintf("[%s] %s: %s", cond.Type, cond.Reason, cond.Message))
	}
	return lines, nil
}

// DeleteSandbox adapts env.Client.DeleteSandbox to match the interface signature.
func (a *SDKAdapter) DeleteSandbox(ctx context.Context, workspace, name string) error {
	err := a.client.DeleteSandbox(ctx, "default", name)
	if err != nil {
		return fmt.Errorf("delete sandbox: %w", err)
	}
	return nil
}

// ListSandboxes adapts env.Client to return sandbox IDs for a workspace.
// Note: the real SDK doesn't expose a direct list-by-workspace method,
// so we fall back to returning known sandboxes or querying via health.
func (a *SDKAdapter) ListSandboxes(ctx context.Context, workspace string) ([]string, error) {
	// For MVP, return empty since SDK doesn't expose listing by workspace.
	return []string{}, nil
}

// KeepAlive verifies the sandbox is still alive and accessible.
// This serves as a heartbeat to prevent idle-timeout deletion during long multi-turn loops.
func (a *SDKAdapter) KeepAlive(ctx context.Context, sandboxName string) error {
	_, err := a.client.GetSandbox(ctx, "default", sandboxName)
	if err != nil {
		return fmt.Errorf("keepalive: %w", err)
	}
	return nil
}

// GetMTLSPaths delegates to the underlying client's mTLS configuration.
func (a *SDKAdapter) GetMTLSPaths() (certPath, keyPath string) {
	return a.client.GetMTLSPaths()
}
