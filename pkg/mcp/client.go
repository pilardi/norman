// Package mcp provides a thin wrapper around the Model Context Protocol SDK
// for discovering and invoking tools from MCP-enabled skills.
package mcp

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ToolDef represents a discovered tool definition from an MCP server.
type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

// Client wraps an MCP client session, providing tool discovery and invocation.
type Client struct {
	mu       sync.RWMutex
	endpoint string
	client   *mcp.Client
	session  *mcp.ClientSession
	tools    []ToolDef
	active   bool
}

// NewClient creates a new MCP client wrapper with the given endpoint URL.
func NewClient(impl *mcp.Implementation, endpoint string) *Client {
	return &Client{
		endpoint: endpoint,
		client:   mcp.NewClient(impl, nil),
		active:   false,
	}
}

// Connect establishes a connection to the MCP endpoint using SSE transport.
func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.active {
		return fmt.Errorf("client already connected")
	}

	transport := &mcp.SSEClientTransport{Endpoint: c.endpoint}
	session, err := c.client.Connect(ctx, transport, nil)
	if err != nil {
		return fmt.Errorf("connect to MCP endpoint %s: %w", c.endpoint, err)
	}

	c.session = session
	c.active = true
	return nil
}

// Disconnect closes the MCP session connection.
func (c *Client) Disconnect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.active {
		return nil
	}

	if err := c.session.Close(); err != nil {
		return fmt.Errorf("close MCP session: %w", err)
	}

	c.session = nil
	c.tools = nil
	c.active = false
	return nil
}

// ListTools discovers available tools from the connected MCP server.
func (c *Client) ListTools(ctx context.Context) ([]ToolDef, error) {
	c.mu.RLock()
	if !c.active || c.session == nil {
		c.mu.RUnlock()
		return nil, fmt.Errorf("client not connected")
	}
	c.mu.RUnlock()

	result, err := c.session.ListTools(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("list tools: %w", err)
	}

	tools := make([]ToolDef, len(result.Tools))
	for i, t := range result.Tools {
		tools[i] = ToolDef{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.InputSchema.(map[string]any),
		}
	}

	c.mu.Lock()
	c.tools = tools
	c.mu.Unlock()

	return tools, nil
}

// CallTool invokes a tool by name with the given arguments.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (*mcp.CallToolResult, error) {
	c.mu.RLock()
	if !c.active || c.session == nil {
		c.mu.RUnlock()
		return nil, fmt.Errorf("client not connected")
	}
	c.mu.RUnlock()

	params := &mcp.CallToolParams{
		Name:      name,
		Arguments: args,
	}

	result, err := c.session.CallTool(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("call tool %q: %w", name, err)
	}

	return result, nil
}

// Active returns whether the client is currently connected.
func (c *Client) Active() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.active
}

// Tools returns the cached tool definitions discovered at last ListTools call.
func (c *Client) Tools() []ToolDef {
	c.mu.RLock()
	defer c.mu.RUnlock()
	tools := make([]ToolDef, len(c.tools))
	copy(tools, c.tools)
	return tools
}

// Endpoints returns the configured endpoint URL.
func (c *Client) Endpoint() string {
	return c.endpoint
}

// SetEndpoint updates the endpoint URL for this client.
func (c *Client) SetEndpoint(endpoint string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.endpoint = endpoint
}

// HTTPClient returns the underlying HTTP client used for transport.
func (c *Client) HTTPClient() *http.Client {
	return nil // exposed for testing; real usage via transport config
}
