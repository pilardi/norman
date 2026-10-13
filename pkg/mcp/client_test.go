package mcp

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestNewClient(t *testing.T) {
	client := NewClient(&mcp.Implementation{Name: "test-client", Version: "v1.0.0"}, "http://localhost:3001/mcp")
	if client == nil {
		t.Fatal("NewClient returned nil")
	}
	if client.Active() {
		t.Error("new client should not be active until connected")
	}
}

func TestSetEndpoint(t *testing.T) {
	client := NewClient(&mcp.Implementation{Name: "test"}, "")
	client.SetEndpoint("http://localhost:3001/mcp")
	if client.Endpoint() != "http://localhost:3001/mcp" {
		t.Errorf("expected endpoint, got %s", client.Endpoint())
	}
}

func TestConnectDisconnect(t *testing.T) {
	client := NewClient(&mcp.Implementation{Name: "test"}, "http://localhost:3001/mcp")

	ctx, cancel := context.WithTimeout(context.Background(), 500)
	defer cancel()

	err := client.Connect(ctx)
	if err != nil {
		t.Logf("connect failed (expected without real server): %v", err)
	} else {
		if !client.Active() {
			t.Error("client should be active after connect")
		}

		err = client.Disconnect(ctx)
		if err != nil {
			t.Fatalf("disconnect failed: %v", err)
		}

		if client.Active() {
			t.Error("client should not be active after disconnect")
		}
	}
}

func TestCallToolNotConnected(t *testing.T) {
	client := NewClient(&mcp.Implementation{Name: "test"}, "http://localhost:3001/mcp")
	_, err := client.CallTool(context.Background(), "mytool", nil)
	if err == nil {
		t.Error("expected error when not connected")
	}
}

func TestListToolsNotConnected(t *testing.T) {
	client := NewClient(&mcp.Implementation{Name: "test"}, "http://localhost:3001/mcp")
	_, err := client.ListTools(context.Background())
	if err == nil {
		t.Error("expected error when not connected")
	}
}
