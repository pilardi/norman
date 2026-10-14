package notify

import (
	"testing"
	"time"

	"github.com/norman/norman/pkg/protocol"
)

// mockWSHub implements WSHubPublisher for testing.
type mockWSHub struct {
	events []protocol.WebSocketEvent
}

func (m *mockWSHub) BroadcastEvent(event protocol.WebSocketEvent) {
	m.events = append(m.events, event)
}

func TestNotifyBasic(t *testing.T) {
	wh := &mockWSHub{}
	n := New(wh, true, "", 10)

	err := n.Notify(Notification{
		TaskID:    "task-123",
		Title:     "Build Complete",
		Body:      "All tests passed successfully",
		Severity:  "info",
		Timestamp: time.Now(),
	})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if len(wh.events) != 1 {
		t.Errorf("expected 1 event, got %d", len(wh.events))
	}
}

func TestNotifyRateLimit(t *testing.T) {
	wh := &mockWSHub{}
	n := New(wh, false, "", 3) // limit of 3/min

	for i := 0; i < 3; i++ {
		err := n.Notify(Notification{TaskID: "task", Title: "test", Body: "msg"})
		if err != nil {
			t.Fatalf("notification %d failed: %v", i, err)
		}
	}

	// Fourth should be rate-limited
	err := n.Notify(Notification{TaskID: "task", Title: "test", Body: "msg"})
	if err == nil {
		t.Error("expected rate limit error")
	}
}

func TestNotifyNoWS(t *testing.T) {
	n := New(nil, false, "", 10) // nil WS hub

	err := n.Notify(Notification{TaskID: "task", Title: "test", Body: "msg"})
	if err != nil {
		t.Fatalf("expected no error with nil WS hub: %v", err)
	}
}

func TestNotifySlack(t *testing.T) {
	wh := &mockWSHub{}
	n := New(wh, false, "https://hooks.slack.com/test", 10)

	err := n.Notify(Notification{TaskID: "task-456", Title: "Deploy", Body: "v2.1 deployed"})
	if err != nil {
		t.Fatalf("expected no error: %v", err)
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		input    string
		maxLen   int
		expected string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "he..."},
		{"", 5, ""},
	}

	for _, tt := range tests {
		result := truncate(tt.input, tt.maxLen)
		if result != tt.expected {
			t.Errorf("truncate(%q, %d) = %q; want %q", tt.input, tt.maxLen, result, tt.expected)
		}
	}
}
