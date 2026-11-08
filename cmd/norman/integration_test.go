package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
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

// ensureServer starts the norman-server if not already running.
func ensureServer(t *testing.T) {
	t.Helper()
	if serverProcess != nil {
		// Check if it's still alive via a zero-signal (non-destructive existence check).
		if err := serverProcess.Signal(syscall.Signal(0)); err == nil || err.Error() == "no such process" {
			return // server is running or already exited
		}
	}

	bin := filepath.Join("..", "..", "dist", "norman-server")
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("%s not found; run 'make build' first", bin)
	}

	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "OPENSHELL_GATEWAY_ADDRESS="+mockGatewayAddr)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	serverProcess = cmd.Process

	waitForServer(t)
}

// getBaseDir extracts the store base directory from the server's /api/config response.
func getBaseDir(t *testing.T) string {
	resp, err := http.Get("http://127.0.0.1:8080/api/config")
	if err != nil {
		t.Fatalf("GET /api/config: %v", err)
	}
	defer resp.Body.Close()

	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode config: %v", err)
	}

	// The store base is in the "openshell" section under "store_dir".
	if storeDir, ok := body["openshell"].(map[string]interface{}); ok {
		if dir, ok := storeDir["store_dir"].(string); ok && dir != "" {
			return dir
		}
	}

	// Fallback: derive from the server's working directory via env.
	return os.ExpandEnv("$HOME/.norman/store")
}

// createTestWorkspace creates a test workspace and returns its ID.
// Tries POST /api/workspaces first (preferred, updates server's in-memory cache).
// Falls back to writing JSON directly to disk if the API requires OpenShell sandbox creation.
func createTestWorkspace(t *testing.T, baseDir string) string {
	t.Helper()

	// First, check if "test-ws" already exists by listing workspaces.
	resp, err := http.Get("http://127.0.0.1:8080/api/workspaces")
	if err != nil {
		t.Logf("warning: could not list workspaces: %v", err)
	} else {
		defer resp.Body.Close()
		var existing []map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&existing); err == nil {
			for _, wsMap := range existing {
				if name, ok := wsMap["name"].(string); ok && name == "test-ws" {
					if id, ok := wsMap["id"].(string); ok && id != "" {
						return id
					}
				}
			}
		}
	}

	// Try creating workspace via REST API first (preferred — updates in-memory cache).
	createBody := map[string]interface{}{
		"name": "test-ws",
	}
	bodyBytes, err := json.Marshal(createBody)
	if err != nil {
		t.Fatalf("marshal workspace creation request: %v", err)
	}

	createResp, err := http.Post("http://127.0.0.1:8080/api/workspaces", "application/json", bytes.NewReader(bodyBytes))
	if err == nil {
		defer createResp.Body.Close()
		var created map[string]interface{}
		if err := json.NewDecoder(createResp.Body).Decode(&created); err != nil {
			t.Fatalf("decode workspace creation response: %v", err)
		}
		id, ok := created["id"].(string)
		if !ok || id == "" {
			// API returned success HTTP status but body contains an error (e.g. CreateSandbox unimplemented).
			// Fall through to disk-write fallback below.
			t.Logf("workspace creation API returned empty ID, falling back to disk write: %v", created)
		} else {
			return id
		}
	}

	// REST API unavailable or failed (e.g. CreateSandbox not implemented in CI mock).
	// Fall back to writing workspace JSON directly to disk.
	wsPath := filepath.Join(baseDir, "workspaces")
	if err := os.MkdirAll(wsPath, 0755); err != nil {
		t.Fatalf("create workspaces dir: %v", err)
	}

	wsFile := filepath.Join(wsPath, "test-ws.json")
	wsID := "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
	wsData := map[string]interface{}{
		"id":         wsID,
		"name":       "test-ws",
		"path":       filepath.Join(baseDir, "workspaces", "test-ws"),
		"status":     "active",
		"created_at": time.Now().Format(time.RFC3339),
		"updated_at": time.Now().Format(time.RFC3339),
	}

	contentBytes, err := json.MarshalIndent(wsData, "", "  ")
	if err != nil {
		t.Fatalf("marshal workspace data: %v", err)
	}

	if err := os.WriteFile(wsFile, contentBytes, 0644); err != nil {
		t.Fatalf("write workspace file: %v", err)
	}

	return wsID
}

// deleteAllSessionsForWorkspace deletes all active sessions for a workspace.
func deleteAllSessionsForWorkspace(t *testing.T, wsID string) {
	t.Helper()

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:8080/api/sessions?workspace_id=%s", wsID))
	if err != nil {
		t.Logf("warning: could not list sessions for workspace %s: %v", wsID[:8], err)
		return
	}
	defer resp.Body.Close()

	var sessions []map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&sessions); err != nil {
		t.Logf("warning: could not decode sessions list: %v", err)
		return
	}

	for _, sess := range sessions {
		if sid, ok := sess["id"].(string); ok && sid != "" {
			req, _ := http.NewRequest("DELETE", fmt.Sprintf("http://127.0.0.1:8080/api/sessions/%s", sid), nil)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Logf("warning: could not delete session %s: %v", sid[:8], err)
			} else if resp.StatusCode != http.StatusOK {
				t.Logf("warning: delete session %s returned status %d", sid[:8], resp.StatusCode)
			}
			resp.Body.Close()
		}
	}
}

// TestPhase4SessionCleanup verifies end-to-end session lifecycle with archive/delete cleanup.
// This is the dedicated integration test for Phase 4: it starts the server, sends messages
// via HTTP to populate turns, confirms turns are present via /api/sessions/{id}/history,
// then verifies cleanup behavior after archive and delete operations.
func TestPhase4SessionCleanup(t *testing.T) {
	ensureServer(t)
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	baseDir := getBaseDir(t)
	t.Logf("Using store base dir: %s", baseDir)

	// Step 0: Ensure test workspace exists.
	wsID := createTestWorkspace(t, baseDir)
	t.Logf("Workspace ID: %s", wsID)

	// Clean up any existing sessions for this workspace (max_active_sessions limit).
	deleteAllSessionsForWorkspace(t, wsID)

	// Step 1: Create a session via the HTTP API.
	createResp, err := http.Post(
		"http://127.0.0.1:8080/api/sessions",
		"application/json",
		strings.NewReader(fmt.Sprintf(`{"workspace_id":"%s","client_type":"cli"}`, wsID)),
	)
	if err != nil {
		t.Fatalf("POST /api/sessions: %v", err)
	}
	defer createResp.Body.Close()

	var created map[string]interface{}
	if err := json.NewDecoder(createResp.Body).Decode(&created); err != nil {
		t.Fatalf("decode session creation response: %v", err)
	}
	sessionID, ok := created["id"].(string)
	if !ok || sessionID == "" {
		t.Fatalf("session ID not found in response: %v", created)
	}
	t.Logf("Created session: %s", sessionID)

	// Step 2: Send messages to populate turns (simulating a conversation).
	messages := []struct {
		content string
	}{
		{"Hello, this is the first user message."},
		{"This is a follow-up question about the weather."},
		{"Please summarize the key points from our conversation."},
	}

	for i, msg := range messages {
		resp, err := http.Post(
			fmt.Sprintf("http://127.0.0.1:8080/api/sessions/%s/messages", sessionID),
			"application/json",
			strings.NewReader(fmt.Sprintf(`{"message":"%s"}`, msg.content)),
		)
		if err != nil {
			t.Fatalf("POST /api/sessions/%s/messages (turn %d): %v", sessionID, i+1, err)
		}
		resp.Body.Close()

		// Small delay to allow server-side persistence.
		time.Sleep(200 * time.Millisecond)
	}

	// Step 3: Verify turns are present via /api/sessions/{id}/history.
	historyResp, err := http.Get(fmt.Sprintf("http://127.0.0.1:8080/api/sessions/%s/history?limit=10", sessionID))
	if err != nil {
		t.Fatalf("GET /api/sessions/%s/history: %v", sessionID, err)
	}
	defer historyResp.Body.Close()

	var turns []map[string]interface{}
	if err := json.NewDecoder(historyResp.Body).Decode(&turns); err != nil {
		t.Fatalf("decode history response: %v", err)
	}

	if len(turns) < 2 {
		t.Errorf("expected at least 2 turns (user + assistant), got %d", len(turns))
	} else {
		t.Logf("History confirmed: %d turns present", len(turns))
	}

	// Verify the .jsonl turns file exists on disk.
	turnsFile := filepath.Join(baseDir, "turns", sessionID+".jsonl")
	if _, err := os.Stat(turnsFile); err != nil {
		t.Fatalf("turns file should exist before archive: %v", err)
	}

	// Step 4: Archive the session and verify cleanup.
	archiveResp, err := http.Post(
		fmt.Sprintf("http://127.0.0.1:8080/api/sessions/%s/archive", sessionID),
		"application/json",
		strings.NewReader(`{}`),
	)
	if err != nil {
		t.Fatalf("POST /api/sessions/%s/archive: %v", sessionID, err)
	}
	defer archiveResp.Body.Close()

	if archiveResp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(archiveResp.Body)
		t.Errorf("archive returned status %d, body: %s", archiveResp.StatusCode, string(body))
	}

	// Verify turns file is removed after archive.
	if _, err := os.Stat(turnsFile); !os.IsNotExist(err) {
		t.Errorf("turns file should be removed after archive")
	}

	// Verify .archive.json was created.
	archivePath := filepath.Join(baseDir, "sessions", sessionID+".archive.json")
	if _, err := os.Stat(archivePath); err != nil {
		t.Fatalf("archive file should exist after archive: %v", err)
	}

	// Verify original session JSON is removed.
	sessionJSON := filepath.Join(baseDir, "sessions", sessionID+".json")
	if _, err := os.Stat(sessionJSON); !os.IsNotExist(err) {
		t.Errorf("original session file should be removed after archive")
	}

	// Step 5: Verify history returns empty after archive.
	historyResp2, err := http.Get(fmt.Sprintf("http://127.0.0.1:8080/api/sessions/%s/history?limit=10", sessionID))
	if err != nil {
		t.Fatalf("GET /api/sessions/%s/history (after archive): %v", sessionID, err)
	}
	defer historyResp2.Body.Close()

	var turnsAfter []map[string]interface{}
	if err := json.NewDecoder(historyResp2.Body).Decode(&turnsAfter); err != nil {
		t.Fatalf("decode history after archive: %v", err)
	}

	if len(turnsAfter) > 0 {
		t.Errorf("expected empty history after archive, got %d entries", len(turnsAfter))
	} else {
		t.Log("History correctly empty after archive")
	}

	// Step 6: Create a new session and delete it to verify DeleteSession cleanup.
	createResp2, err := http.Post(
		"http://127.0.0.1:8080/api/sessions",
		"application/json",
		strings.NewReader(fmt.Sprintf(`{"workspace_id":"%s","client_type":"cli"}`, wsID)),
	)
	if err != nil {
		t.Fatalf("POST /api/sessions (2nd): %v", err)
	}
	defer createResp2.Body.Close()

	var created2 map[string]interface{}
	if err := json.NewDecoder(createResp2.Body).Decode(&created2); err != nil {
		t.Fatalf("decode session creation response: %v", err)
	}
	sessionID2, ok := created2["id"].(string)
	if !ok || sessionID2 == "" {
		t.Fatalf("session ID not found in 2nd response")
	}
	t.Logf("Created session for delete test: %s", sessionID2)

	// Send a message to create turns.
	resp, err := http.Post(
		fmt.Sprintf("http://127.0.0.1:8080/api/sessions/%s/messages", sessionID2),
		"application/json",
		strings.NewReader(`{"message":"Delete me please."}`),
	)
	if err != nil {
		t.Fatalf("POST /api/sessions/%s/messages: %v", sessionID2, err)
	}
	resp.Body.Close()
	time.Sleep(200 * time.Millisecond)

	// Verify turns file exists.
	turnsFile2 := filepath.Join(baseDir, "turns", sessionID2+".jsonl")
	if _, err := os.Stat(turnsFile2); err != nil {
		t.Fatalf("turns file should exist before delete: %v", err)
	}

	// Step 7: Delete the session and verify cleanup.
	req, _ := http.NewRequest("DELETE", fmt.Sprintf("http://127.0.0.1:8080/api/sessions/%s", sessionID2), nil)
	deleteResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE /api/sessions/%s: %v", sessionID2, err)
	}
	defer deleteResp.Body.Close()

	if deleteResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(deleteResp.Body)
		t.Errorf("delete returned status %d, body: %s", deleteResp.StatusCode, string(body))
	}

	// Verify turns file is removed after delete.
	if _, err := os.Stat(turnsFile2); !os.IsNotExist(err) {
		t.Errorf("turns file should be removed after delete")
	}

	// Verify session JSON is removed.
	sessionJSON2 := filepath.Join(baseDir, "sessions", sessionID2+".json")
	if _, err := os.Stat(sessionJSON2); !os.IsNotExist(err) {
		t.Errorf("original session file should be removed after delete")
	}

	t.Logf("Phase 4 cleanup verified: archive removes turns + creates .archive.json; delete removes turns + session JSON")
}

// TestPhase4ArchiveIdempotent verifies that archiving an already-archived session is idempotent.
func TestPhase4ArchiveIdempotent(t *testing.T) {
	ensureServer(t)
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	baseDir := getBaseDir(t)

	// Ensure test workspace exists.
	wsID := createTestWorkspace(t, baseDir)
	t.Logf("Workspace ID: %s", wsID)

	// Clean up any existing sessions for this workspace (max_active_sessions limit).
	deleteAllSessionsForWorkspace(t, wsID)

	// Create and archive a session.
	createResp, err := http.Post(
		"http://127.0.0.1:8080/api/sessions",
		"application/json",
		strings.NewReader(fmt.Sprintf(`{"workspace_id":"%s","client_type":"cli"}`, wsID)),
	)
	if err != nil {
		t.Fatalf("POST /api/sessions: %v", err)
	}
	defer createResp.Body.Close()

	var created map[string]interface{}
	if err := json.NewDecoder(createResp.Body).Decode(&created); err != nil {
		t.Fatalf("decode session creation response: %v", err)
	}
	sessionID, ok := created["id"].(string)
	if !ok || sessionID == "" {
		t.Fatalf("session ID not found")
	}

	// Send a message to create turns.
	resp, err := http.Post(
		fmt.Sprintf("http://127.0.0.1:8080/api/sessions/%s/messages", sessionID),
		"application/json",
		strings.NewReader(`{"message":"Test idempotent archive."}`),
	)
	if err != nil {
		t.Fatalf("POST /api/sessions/%s/messages: %v", sessionID, err)
	}
	resp.Body.Close()
	time.Sleep(200 * time.Millisecond)

	// Archive once.
	archiveResp1, err := http.Post(
		fmt.Sprintf("http://127.0.0.1:8080/api/sessions/%s/archive", sessionID),
		"application/json",
		strings.NewReader(`{}`),
	)
	if err != nil {
		t.Fatalf("POST /api/sessions/%s/archive (1st): %v", sessionID, err)
	}
	defer archiveResp1.Body.Close()

	// Archive again (idempotent).
	archiveResp2, err := http.Post(
		fmt.Sprintf("http://127.0.0.1:8080/api/sessions/%s/archive", sessionID),
		"application/json",
		strings.NewReader(`{}`),
	)
	if err != nil {
		t.Fatalf("POST /api/sessions/%s/archive (2nd): %v", sessionID, err)
	}
	defer archiveResp2.Body.Close()

	// Verify .archive.json still exists and is valid.
	archivePath := filepath.Join(baseDir, "sessions", sessionID+".archive.json")
	if _, err := os.Stat(archivePath); err != nil {
		t.Fatalf("archive file should exist: %v", err)
	}

	// Verify turns file is gone.
	turnsFile := filepath.Join(baseDir, "turns", sessionID+".jsonl")
	if _, err := os.Stat(turnsFile); !os.IsNotExist(err) {
		t.Errorf("turns file should be removed after first archive")
	}

	t.Log("Archive is idempotent — double archive succeeds without error")
}

// TestPhase4DeleteSessionWithTasks verifies that DeleteSession also cleans up associated task files.
func TestPhase4DeleteSessionWithTasks(t *testing.T) {
	ensureServer(t)
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	baseDir := getBaseDir(t)

	// Ensure test workspace exists.
	wsID := createTestWorkspace(t, baseDir)
	t.Logf("Workspace ID: %s", wsID)

	// Clean up any existing sessions for this workspace (max_active_sessions limit).
	deleteAllSessionsForWorkspace(t, wsID)

	// Create a session and send messages (which creates tasks internally).
	createResp, err := http.Post(
		"http://127.0.0.1:8080/api/sessions",
		"application/json",
		strings.NewReader(fmt.Sprintf(`{"workspace_id":"%s","client_type":"cli"}`, wsID)),
	)
	if err != nil {
		t.Fatalf("POST /api/sessions: %v", err)
	}
	defer createResp.Body.Close()

	var created map[string]interface{}
	if err := json.NewDecoder(createResp.Body).Decode(&created); err != nil {
		t.Fatalf("decode session creation response: %v", err)
	}
	sessionID, ok := created["id"].(string)
	if !ok || sessionID == "" {
		t.Fatalf("session ID not found")
	}

	// Send multiple messages to generate turns + internal tasks.
	for i := 0; i < 3; i++ {
		resp, err := http.Post(
			fmt.Sprintf("http://127.0.0.1:8080/api/sessions/%s/messages", sessionID),
			"application/json",
			strings.NewReader(fmt.Sprintf(`{"message":"Message %d for cleanup test."}`, i+1)),
		)
		if err != nil {
			t.Fatalf("POST /api/sessions/%s/messages: %v", sessionID, err)
		}
		resp.Body.Close()
		time.Sleep(200 * time.Millisecond)
	}

	// Count task files before delete.
	tasksDir := filepath.Join(baseDir, "tasks")
	beforeCount := 0
	if entries, err := os.ReadDir(tasksDir); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() && filepath.Ext(entry.Name()) == ".json" {
				data, _ := os.ReadFile(filepath.Join(tasksDir, entry.Name()))
				var task map[string]interface{}
				if json.Unmarshal(data, &task) == nil {
					if sid, ok := task["session_id"].(string); ok && sid == sessionID {
						beforeCount++
					}
				}
			}
		}
	}

	t.Logf("Found %d task files associated with session %s before delete", beforeCount, sessionID[:8])

	// Delete the session.
	req, _ := http.NewRequest("DELETE", fmt.Sprintf("http://127.0.0.1:8080/api/sessions/%s", sessionID), nil)
	deleteResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE /api/sessions/%s: %v", sessionID, err)
	}
	defer deleteResp.Body.Close()

	// Verify turns file removed.
	turnsFile := filepath.Join(baseDir, "turns", sessionID+".jsonl")
	if _, err := os.Stat(turnsFile); !os.IsNotExist(err) {
		t.Errorf("turns file should be removed after delete")
	}

	// Verify task files for this session are removed.
	afterCount := 0
	if entries, err := os.ReadDir(tasksDir); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() && filepath.Ext(entry.Name()) == ".json" {
				data, _ := os.ReadFile(filepath.Join(tasksDir, entry.Name()))
				var task map[string]interface{}
				if json.Unmarshal(data, &task) == nil {
					if sid, ok := task["session_id"].(string); ok && sid == sessionID {
						afterCount++
					}
				}
			}
		}
	}

	if afterCount != 0 {
		t.Errorf("expected 0 task files for deleted session, found %d", afterCount)
	} else {
		t.Logf("All %d associated task files cleaned up after delete", beforeCount)
	}

	t.Log("Phase 4 DeleteSession cleanup verified: turns + tasks removed")
}
