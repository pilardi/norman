package main

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestClientServerCommunication verifies the client can reach and communicate
// with the server process. This catches regressions like a missing e.Start()
// call or a response body being closed before it is read.
func TestClientServerCommunication(t *testing.T) {
	t.Parallel()

	bin := filepath.Join("..", "..", "dist", "norman-server")
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("%s not found; run 'make build' first", bin)
	}

	cmd := exec.Command(bin)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer func() {
		_ = cmd.Process.Signal(os.Interrupt)
		_ = cmd.Wait()
	}()

	// Wait for the server to be ready.
	for i := 0; i < 30; i++ {
		if resp, err := http.Get("http://127.0.0.1:8080/health"); err == nil {
			resp.Body.Close()
			return // server is up
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatal("server did not become ready within timeout")
}

// TestHealthEndpoint verifies the /health endpoint returns valid JSON.
func TestHealthEndpoint(t *testing.T) {
	t.Parallel()

	resp, err := http.Get("http://127.0.0.1:8080/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()

	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode health response: %v", err)
	}

	if status, ok := body["status"].(string); !ok || (status != "ok" && status != "healthy") {
		t.Errorf("expected status=ok or healthy, got %v", body["status"])
	}
}

// TestConfigEndpoint verifies the /api/config endpoint returns a valid config object.
func TestConfigEndpoint(t *testing.T) {
	t.Parallel()

	resp, err := http.Get("http://127.0.0.1:8080/api/config")
	if err != nil {
		t.Fatalf("GET /api/config: %v", err)
	}
	defer resp.Body.Close()

	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode config response: %v", err)
	}

	// Verify top-level keys exist.
	for _, key := range []string{"system", "openshell"} {
		if _, ok := body[key]; !ok {
			t.Errorf("missing top-level key %q in config response", key)
		}
	}

	// Verify system fields.
	sys, ok := body["system"].(map[string]interface{})
	if !ok {
		t.Fatal("system field is not a map")
	}
	for _, field := range []string{"Host", "ServerPort", "DefaultProvider"} {
		if _, ok := sys[field]; !ok {
			t.Errorf("missing system field %q", field)
		}
	}
}

// TestCLIConfigJSON verifies the CLI binary's config --json output parses as valid JSON.
func TestCLIConfigJSON(t *testing.T) {
	t.Parallel()

	bin := filepath.Join("..", "..", "dist", "norman")
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("%s not found; run 'make build' first", bin)
	}

	cmd := exec.Command(bin, "config", "--json")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("norman config --json: %v\n%s", err, string(out))
	}

	var body map[string]interface{}
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatalf("output is not valid JSON:\n%s", string(out))
	}

	// Quick sanity check on a known field.
	if _, ok := body["host"]; !ok {
		t.Errorf("expected 'host' key in config output")
	}
}

// TestCLIPlaintextConfig verifies the CLI binary's plain text config output is non-empty.
func TestCLIPlaintextConfig(t *testing.T) {
	t.Parallel()

	bin := filepath.Join("..", "..", "dist", "norman")
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("%s not found; run 'make build' first", bin)
	}

	cmd := exec.Command(bin, "config")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("norman config: %v\n%s", err, string(out))
	}

	if len(strings.TrimSpace(string(out))) == 0 {
		t.Fatal("config output is empty")
	}

	// Verify it contains expected section headers.
	output := string(out)
	for _, marker := range []string{"Current Configuration", "server_port", "host"} {
		if !strings.Contains(output, marker) {
			t.Errorf("expected '%s' in config output", marker)
		}
	}
}
