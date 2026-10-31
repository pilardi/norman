// Package notify provides real-time notification delivery for Norman.
//
// Notifiers push task results to connected WebSocket clients, ring a terminal bell,
// and optionally post to a Slack webhook. Rate limiting is enforced via a token bucket (10/min).
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/norman/norman/pkg/protocol"
)

// Notification represents a single notification to be delivered.
type Notification struct {
	TaskID    string `json:"task_id"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	Severity  string `json:"severity"` // "info", "warn", "error"
	Timestamp time.Time
}

// Notifier delivers notifications via multiple channels.
type Notifier struct {
	mu sync.Mutex
	// WS hub reference — push structured events to connected clients
	wsHub WSHubPublisher
	// Terminal bell enabled flag
	bellEnabled bool
	// Slack webhook URL (empty = disabled)
	slackURL string
	// Rate limiting: token bucket, 10 tokens per minute
	rateLimit  int
	tokens     float64
	lastRefill time.Time
	// timeout is the maximum wait duration for AskUser/AskUserBlocking.
	timeout time.Duration
}

// WSHubPublisher interface for pushing WebSocket events and routing responses.
type WSHubPublisher interface {
	BroadcastEvent(event protocol.WebSocketEvent)
	// RegisterResponseChannel creates a channel for blocking user-response calls.
	// Returns a request ID string and the response channel.
	RegisterResponseChannel() (string, chan string)
	// SendResponse routes a message to a registered response channel.
	SendResponse(id string, message string) bool
}

// WithTimeout sets the maximum wait duration for AskUser/AskUserBlocking.
// Default is 60 seconds if not specified.
func WithTimeout(dur time.Duration) func(*Notifier) {
	return func(n *Notifier) {
		n.timeout = dur
	}
}

// New creates a Notifier with the given dependencies and optional configuration.
func New(wsHub WSHubPublisher, bellEnabled bool, slackURL string, rateLimitPerMinute int, opts ...func(*Notifier)) *Notifier {
	if rateLimitPerMinute <= 0 {
		rateLimitPerMinute = 10 // default
	}
	n := &Notifier{
		wsHub:       wsHub,
		bellEnabled: bellEnabled,
		slackURL:    slackURL,
		rateLimit:   rateLimitPerMinute,
		tokens:      float64(rateLimitPerMinute),
		lastRefill:  time.Now(),
		timeout:     60 * time.Second, // default timeout
	}
	for _, opt := range opts {
		opt(n)
	}
	return n
}

// Notify sends a notification through all configured channels.
// Returns an error if the rate limit is exceeded or delivery fails.
func (n *Notifier) Notify(notification Notification) error {
	// Check rate limit (token bucket)
	if !n.consumeToken() {
		return fmt.Errorf("rate limit exceeded: max %d notifications/minute", n.rateLimit)
	}

	// 1. Push to WebSocket hub
	n.pushWS(notification)

	// 2. Ring terminal bell if enabled
	if n.bellEnabled {
		n.ringBell(notification.Title, notification.Body)
	}

	// 3. Post to Slack webhook if configured
	if n.slackURL != "" {
		go n.postSlack(notification) // non-blocking
	}

	return nil
}

// consumeToken implements the token bucket rate limiter.
func (n *Notifier) consumeToken() bool {
	n.mu.Lock()
	defer n.mu.Unlock()

	now := time.Now()
	// Refill tokens based on elapsed time (smooth leak)
	elapsed := now.Sub(n.lastRefill).Seconds()
	n.tokens += elapsed * float64(n.rateLimit) / 60.0
	if n.tokens > float64(n.rateLimit) {
		n.tokens = float64(n.rateLimit)
	}
	n.lastRefill = now

	if n.tokens < 1.0 {
		return false // no tokens available
	}

	n.tokens -= 1.0
	return true
}

// pushWS sends a structured WebSocketEvent to all connected clients.
func (n *Notifier) pushWS(notification Notification) {
	if n.wsHub == nil {
		return
	}

	event := protocol.WebSocketEvent{
		Event:    protocol.Notification,
		TaskId:   &notification.TaskID,
		Message:  strPtr(fmt.Sprintf("[%s] %s: %s", notification.Severity, notification.Title, notification.Body)),
		Priority: priority(notification.Severity),
	}

	n.wsHub.BroadcastEvent(event)
}

// AskUser sends a question to the connected user and waits for their response.
// It broadcasts a notification event via WebSocket and blocks until a response
// is received on the provided response channel (populated by the client).
// Returns an error if no response arrives within the timeout (default 60s).
func (n *Notifier) AskUser(question string, taskID string, respCh chan string) (string, error) {
	notification := Notification{
		TaskID:    taskID,
		Title:     "Question for you",
		Body:      question,
		Severity:  "info",
		Timestamp: time.Now(),
	}

	n.pushWS(notification)

	// Wait for response (non-rate-limited for ask_user to avoid blocking user)
	select {
	case resp := <-respCh:
		return resp, nil
	case <-time.After(n.timeout):
		return "", fmt.Errorf("ask_user: no response within %s", n.timeout.String())
	}
}

// AskUserBlocking asks the connected user a question and blocks until their
// response is received via a WebSocket response channel. This is a simplified
// single-argument variant of AskUser that manages the response channel internally.
// Returns an error if no response arrives within the timeout (default 60s).
func (n *Notifier) AskUserBlocking(question string) (string, error) {
	notification := Notification{
		TaskID:    "", // No taskID needed for blocking variant
		Title:     "Question for you",
		Body:      question,
		Severity:  "info",
		Timestamp: time.Now(),
	}

	n.pushWS(notification)

	// Register a response channel via the WS hub so incoming client messages
	// can be routed here. The hub's SendResponse() populates this channel.
	if n.wsHub != nil {
		_, respCh := n.wsHub.RegisterResponseChannel()

		select {
		case resp := <-respCh:
			return resp, nil
		case <-time.After(n.timeout):
			return "", fmt.Errorf("ask_user_blocking: no response within %s", n.timeout.String())
		}
	}

	// Fallback: if wsHub is nil (e.g., in tests), use a local channel that
	// never receives a message, so the call will timeout as expected.
	respCh := make(chan string, 1)
	select {
	case resp := <-respCh:
		return resp, nil
	case <-time.After(n.timeout):
		return "", fmt.Errorf("ask_user_blocking: no response within %s", n.timeout.String())
	}
}

// Push sends a notification via all configured channels (WebSocket, bell, Slack).
// This is the agent-initiated notification tool — equivalent to Notify(Notification)
// but accepts typed string arguments for direct LLM tool calls.
func (n *Notifier) Push(title string, body string) error {
	if !n.consumeToken() {
		return fmt.Errorf("rate limit exceeded: max %d notifications/minute", n.rateLimit)
	}

	n.mu.Lock()
	defer n.mu.Unlock()

	notification := Notification{
		Title:     title,
		Body:      body,
		Severity:  "info",
		Timestamp: time.Now(),
	}

	n.pushWS(notification)
	return nil
}

// ringBell prints a bell character and brief text to stdout.
func (n *Notifier) ringBell(title, body string) {
	fmt.Print("\a") // ASCII bell character (0x07)
	log.Printf("[bell] %s: %s", title, truncate(body, 80))
}

// postSlack sends a notification to a Slack webhook URL.
func (n *Notifier) postSlack(notification Notification) {
	payload := map[string]interface{}{
		"blocks": []map[string]interface{}{
			{
				"type": "section",
				"text": map[string]interface{}{
					"type": "mrkdwn",
					"text": fmt.Sprintf("*%s*\n%s", notification.Title, truncate(notification.Body, 200)),
				},
			},
		},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		log.Printf("notify: slack marshal error: %v", err)
		return
	}

	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		n.slackURL,
		bytes.NewBuffer(data),
	)
	if err != nil {
		log.Printf("notify: slack request error: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("notify: slack POST failed: %v (webhook may be unreachable)", err)
		return
	}
	_ = resp.Body.Close()

	if resp.StatusCode >= 400 {
		log.Printf("notify: slack returned HTTP %d for task %s", resp.StatusCode, notification.TaskID)
	} else {
		log.Printf("notify: slack notification sent for task %s", notification.TaskID)
	}
}

// strPtr returns a pointer to the string value.
func strPtr(s string) *string { return &s }

// priority maps severity to WebSocketEventPriority.
func priority(severity string) *protocol.WebSocketEventPriority {
	p := protocol.WebSocketEventPriorityNormal
	switch severity {
	case "error":
		p = protocol.WebSocketEventPriorityHigh
	}
	return &p
}

// truncate limits a string to maxLen characters, appending "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
