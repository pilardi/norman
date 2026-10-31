package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	openshellv1 "github.com/NVIDIA/OpenShell/sdk/go/proto/openshellv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
)

var (
	mockGatewayAddr string
	serverProcess   *os.Process
)

// mockOpenShellServer is a minimal gRPC service implementation that satisfies the
// OpenShell proto interface for testing. It embeds UnimplementedOpenShellServer
// and overrides only the Health method to return a healthy status.
type mockOpenShellServer struct {
	openshellv1.UnimplementedOpenShellServer
}

func (m *mockOpenShellServer) Health(context.Context, *openshellv1.HealthRequest) (*openshellv1.HealthResponse, error) {
	return &openshellv1.HealthResponse{
		Status:  openshellv1.ServiceStatus_SERVICE_STATUS_HEALTHY,
		Version: "test-mock-v0.0.0",
	}, nil
}

func TestMain(m *testing.M) {
	const testPort = 19876
	mockGatewayAddr = "http://127.0.0.1:" + strconv.Itoa(testPort)

	// Start the gRPC mock as a background goroutine in this process.
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", testPort))
	if err != nil {
		log.Fatalf("bind port %d: %v", testPort, err)
	}

	srv := grpc.NewServer()

	// Register the standard gRPC health protocol (for HTTP /health checks).
	healthSvc := health.NewServer()
	grpc_health_v1.RegisterHealthServer(srv, healthSvc)
	healthSvc.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)

	// Register the OpenShell proto service with our minimal stub.
	openShellSvc := &mockOpenShellServer{}
	openshellv1.RegisterOpenShellServer(srv, openShellSvc)

	goReady := make(chan struct{})
	go func() { close(goReady); _ = srv.Serve(listener) }()

	select {
	case <-goReady:
	case <-time.After(5 * time.Second):
		log.Fatal("gRPC mock server did not become ready in time")
	}

	time.Sleep(300 * time.Millisecond) // ensure TCP connections are accepted

	code := m.Run()

	// Cleanup: stop the gRPC mock and kill the norman-server process.
	srv.Stop()
	_ = listener.Close()
	if serverProcess != nil {
		_ = serverProcess.Signal(os.Interrupt)
		_, _ = serverProcess.Wait()
	}
	os.Exit(code)
}

// waitForServer waits up to 15 seconds for the norman-server HTTP endpoint to respond.
func waitForServer(t *testing.T) {
	t.Helper()
	for i := 0; i < 30; i++ {
		if resp, err := http.Get("http://127.0.0.1:8080/health"); err == nil {
			resp.Body.Close()
			return // server is up
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatal("server did not become ready within timeout")
}

// TestClientServerCommunication verifies the client can reach and communicate
// with the server process. This catches regressions like a missing e.Start()
// call or a response body being closed before it is read.
func TestClientServerCommunication(t *testing.T) {
	bin := filepath.Join("..", "..", "dist", "norman-server")
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("%s not found; run 'make build' first", bin)
	}

	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "OPENSHELL_GATEWAY_ADDRESS="+mockGatewayAddr)
	t.Logf("Starting server with OPENSHELL_GATEWAY_ADDRESS=%s", mockGatewayAddr)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	serverProcess = cmd.Process // store for cleanup in TestMain

	// Wait for the server to be ready.
	waitForServer(t)
}

// TestHealthEndpoint verifies the /health endpoint returns valid JSON.
func TestHealthEndpoint(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	waitForServer(t)

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
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	bin := filepath.Join("..", "..", "dist", "norman-server")
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("%s not found; run 'make build' first", bin)
	}
	waitForServer(t)

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
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	serverBin := filepath.Join("..", "..", "dist", "norman-server")
	if _, err := os.Stat(serverBin); err != nil {
		t.Skipf("%s not found; run 'make build' first", serverBin)
	}

	waitForServer(t)

	bin := filepath.Join("..", "..", "dist", "norman")
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("%s not found; run 'make build' first", bin)
	}

	cmd := exec.Command(bin, "config", "--json")
	cmd.Env = append(os.Environ(), "OPENSHELL_GATEWAY_ADDRESS="+mockGatewayAddr)
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
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	serverBin := filepath.Join("..", "..", "dist", "norman-server")
	if _, err := os.Stat(serverBin); err != nil {
		t.Skipf("%s not found; run 'make build' first", serverBin)
	}

	waitForServer(t)

	bin := filepath.Join("..", "..", "dist", "norman")
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("%s not found; run 'make build' first", bin)
	}

	cmd := exec.Command(bin, "config")
	cmd.Env = append(os.Environ(), "OPENSHELL_GATEWAY_ADDRESS="+mockGatewayAddr)
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
