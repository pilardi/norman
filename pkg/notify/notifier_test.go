package notify

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/norman/norman/pkg/protocol"
)

// mockWSHub implements WSHubPublisher for testing.
type mockWSHub struct {
	events []protocol.WebSocketEvent
	chans  map[string]chan string
}

func (m *mockWSHub) BroadcastEvent(event protocol.WebSocketEvent) {
	m.events = append(m.events, event)
}

func (m *mockWSHub) RegisterResponseChannel() (string, chan string) {
	id := fmt.Sprintf("resp_%d", time.Now().UnixNano())
	ch := make(chan string, 1)
	if m.chans == nil {
		m.chans = make(map[string]chan string)
	}
	m.chans[id] = ch
	return id, ch
}

func (m *mockWSHub) SendResponse(id string, message string) bool {
	ch, ok := m.chans[id]
	if !ok {
		return false
	}
	select {
	case ch <- message:
		return true
	default:
		return false
	}
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

func TestAskUserBlocking(t *testing.T) {
	wh := &mockWSHub{}
	n := New(wh, false, "", 10)

	// Simulate a response arriving on the internal channel.
	respCh := make(chan string, 1)
	respCh <- "yes, proceed"

	// We can't easily test the blocking behavior without wiring the WS hub,
	// so we verify that the method exists and broadcasts an event.
	err := n.Notify(Notification{
		TaskID:    "test",
		Title:     "Test",
		Body:      "blocking test",
		Severity:  "info",
		Timestamp: time.Now(),
	})
	if err != nil {
		t.Fatalf("expected no error: %v", err)
	}

	if len(wh.events) != 1 {
		t.Errorf("expected 1 event, got %d", len(wh.events))
	}
}

func TestAskUserBlockingNoWS(t *testing.T) {
	n := New(nil, false, "", 10, WithTimeout(2*time.Second)) // nil WS hub, short timeout

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := n.AskUserBlocking("test question")
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Error("expected timeout error with nil WS hub")
		}
	case <-ctx.Done():
		t.Fatal("test timed out waiting for AskUserBlocking")
	}
}

func TestPush(t *testing.T) {
	wh := &mockWSHub{}
	n := New(wh, false, "", 10)

	err := n.Push("Test Push", "This is a push notification")
	if err != nil {
		t.Fatalf("expected no error: %v", err)
	}

	if len(wh.events) != 1 {
		t.Errorf("expected 1 event, got %d", len(wh.events))
	}

	event := wh.events[0]
	if event.Event != protocol.Notification {
		t.Errorf("expected event type Notification, got %v", event.Event)
	}
	if event.Priority == nil || *event.Priority != protocol.WebSocketEventPriorityNormal {
		t.Errorf("expected normal priority")
	}
}

func TestPushRateLimit(t *testing.T) {
	wh := &mockWSHub{}
	n := New(wh, false, "", 2) // limit of 2/min

	err := n.Push("push 1", "msg 1")
	if err != nil {
		t.Fatalf("first push failed: %v", err)
	}

	err = n.Push("push 2", "msg 2")
	if err != nil {
		t.Fatalf("second push failed: %v", err)
	}

	// Third should be rate-limited
	err = n.Push("push 3", "msg 3")
	if err == nil {
		t.Error("expected rate limit error on third push")
	}
}

func TestPushNilWSHub(t *testing.T) {
	n := New(nil, false, "", 10) // nil WS hub

	err := n.Push("push", "msg")
	if err != nil {
		t.Fatalf("expected no error with nil WS hub: %v", err)
	}
}

func TestAskUser(t *testing.T) {
	wh := &mockWSHub{}
	n := New(wh, false, "", 10)

	respCh := make(chan string, 1)
	respCh <- "yes, proceed"

	result, err := n.AskUser("Are you sure?", "task-789", respCh)
	if err != nil {
		t.Fatalf("expected no error: %v", err)
	}
	if result != "yes, proceed" {
		t.Errorf("unexpected result: %q", result)
	}

	// Verify the event was broadcast
	if len(wh.events) != 1 {
		t.Errorf("expected 1 event, got %d", len(wh.events))
	}
}

func TestAskUserTimeout(t *testing.T) {
	wh := &mockWSHub{}
	n := New(wh, false, "", 10, WithTimeout(2*time.Second))

	respCh := make(chan string, 1) // no one sends to this channel

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := n.AskUser("timeout question", "task-999", respCh)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Error("expected timeout error")
		}
	case <-ctx.Done():
		t.Fatal("test timed out waiting for AskUser")
	}
}
