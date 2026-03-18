package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ideamans/eternal/pkg/protocol"
)

// setupTestServer creates a test HTTP server backed by a real Server.
// Sessions use real PTY processes (cat command) for true integration testing.
func setupTestServer(t *testing.T) (*httptest.Server, *Server) {
	t.Helper()
	s := New()
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts, s
}

// createSession creates a session via the REST API and returns the parsed response.
type sessionResponse struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Command []string `json:"command"`
}

func createSession(t *testing.T, ts *httptest.Server, command []string, name string) sessionResponse {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"name":    name,
		"command": command,
		"cols":    80,
		"rows":    24,
	})
	resp, err := http.Post(ts.URL+"/api/sessions", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /api/sessions failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /api/sessions status = %d, want 201", resp.StatusCode)
	}
	var sess sessionResponse
	json.NewDecoder(resp.Body).Decode(&sess)
	return sess
}

// connectWS establishes a WebSocket connection to a session.
func connectWS(t *testing.T, ts *httptest.Server, sessionID string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/session/" + sessionID
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket dial failed: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// wsReader continuously reads from a WebSocket in the background.
type wsReader struct {
	ch chan protocol.Message
}

func newWSReader(t *testing.T, conn *websocket.Conn) *wsReader {
	t.Helper()
	r := &wsReader{ch: make(chan protocol.Message, 100)}
	go func() {
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				close(r.ch)
				return
			}
			var msg protocol.Message
			if json.Unmarshal(raw, &msg) == nil {
				r.ch <- msg
			}
		}
	}()
	return r
}

// collect drains buffered messages and waits up to timeout for more.
func (r *wsReader) collect(timeout time.Duration) []protocol.Message {
	var msgs []protocol.Message
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case msg, ok := <-r.ch:
			if !ok {
				return msgs
			}
			msgs = append(msgs, msg)
			// Reset timer to keep collecting if messages are flowing
			timer.Reset(timeout)
		case <-timer.C:
			return msgs
		}
	}
}

// waitFor waits for a message matching the predicate, up to timeout.
func (r *wsReader) waitFor(timeout time.Duration, pred func(protocol.Message) bool) *protocol.Message {
	deadline := time.After(timeout)
	for {
		select {
		case msg, ok := <-r.ch:
			if !ok {
				return nil
			}
			if pred(msg) {
				return &msg
			}
		case <-deadline:
			return nil
		}
	}
}

// sendInput sends a typed input message via WebSocket.
func sendInput(t *testing.T, conn *websocket.Conn, data string) {
	t.Helper()
	msg := protocol.Message{Type: protocol.TypeInput, Data: []byte(data)}
	raw, _ := json.Marshal(msg)
	if err := conn.WriteMessage(websocket.TextMessage, raw); err != nil {
		t.Fatalf("send input failed: %v", err)
	}
}

// sendResize sends a resize message via WebSocket.
func sendResize(t *testing.T, conn *websocket.Conn, cols, rows int) {
	t.Helper()
	msg := protocol.Message{Type: protocol.TypeResize, Cols: cols, Rows: rows}
	raw, _ := json.Marshal(msg)
	if err := conn.WriteMessage(websocket.TextMessage, raw); err != nil {
		t.Fatalf("send resize failed: %v", err)
	}
}

// --- Integration Tests ---

func TestIntegration_CreateListKill(t *testing.T) {
	ts, _ := setupTestServer(t)

	// Create two sessions
	s1 := createSession(t, ts, []string{"cat"}, "session-a")
	s2 := createSession(t, ts, []string{"cat"}, "session-b")

	// List
	resp, _ := http.Get(ts.URL + "/api/sessions")
	var sessions []sessionResponse
	json.NewDecoder(resp.Body).Decode(&sessions)
	resp.Body.Close()

	if len(sessions) != 2 {
		t.Fatalf("list len = %d, want 2", len(sessions))
	}

	// Get individual session
	resp, _ = http.Get(ts.URL + "/api/sessions/" + s1.ID)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET session status = %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()

	// Kill session
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/sessions/"+s1.ID, nil)
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("DELETE session status = %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()

	time.Sleep(100 * time.Millisecond)

	// Verify only one remains
	resp, _ = http.Get(ts.URL + "/api/sessions")
	json.NewDecoder(resp.Body).Decode(&sessions)
	resp.Body.Close()

	if len(sessions) != 1 {
		t.Errorf("list after kill = %d, want 1", len(sessions))
	}
	if sessions[0].ID != s2.ID {
		t.Errorf("remaining session = %s, want %s", sessions[0].ID, s2.ID)
	}

	// Kill the second one too
	req, _ = http.NewRequest(http.MethodDelete, ts.URL+"/api/sessions/"+s2.ID, nil)
	http.DefaultClient.Do(req)
}

func TestIntegration_WebSocketIO(t *testing.T) {
	ts, _ := setupTestServer(t)

	sess := createSession(t, ts, []string{"cat"}, "echo-test")
	conn := connectWS(t, ts, sess.ID)
	reader := newWSReader(t, conn)

	// Drain initial messages
	reader.collect(300 * time.Millisecond)

	// Send input - cat echoes it back
	sendInput(t, conn, "hello\n")

	msg := reader.waitFor(2*time.Second, func(m protocol.Message) bool {
		return m.Type == protocol.TypeOutput && strings.Contains(string(m.Data), "hello")
	})
	if msg == nil {
		t.Error("did not receive echoed output from cat")
	}

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/sessions/"+sess.ID, nil)
	http.DefaultClient.Do(req)
}

func TestIntegration_WebSocketResize(t *testing.T) {
	ts, _ := setupTestServer(t)

	sess := createSession(t, ts, []string{"cat"}, "resize-test")
	conn1 := connectWS(t, ts, sess.ID)
	r1 := newWSReader(t, conn1)
	r1.collect(300 * time.Millisecond)

	conn2 := connectWS(t, ts, sess.ID)
	r2 := newWSReader(t, conn2)
	r2.collect(300 * time.Millisecond)

	sendResize(t, conn1, 120, 40)

	msg := r2.waitFor(2*time.Second, func(m protocol.Message) bool {
		return m.Type == protocol.TypeResize && m.Cols == 120 && m.Rows == 40
	})
	if msg == nil {
		t.Error("client 2 did not receive resize broadcast from client 1")
	}

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/sessions/"+sess.ID, nil)
	http.DefaultClient.Do(req)
}

func TestIntegration_MultipleClientsShareOutput(t *testing.T) {
	ts, _ := setupTestServer(t)

	sess := createSession(t, ts, []string{"cat"}, "multi-test")
	conn1 := connectWS(t, ts, sess.ID)
	conn2 := connectWS(t, ts, sess.ID)

	r1 := newWSReader(t, conn1)
	r2 := newWSReader(t, conn2)
	r1.collect(300 * time.Millisecond)
	r2.collect(300 * time.Millisecond)

	sendInput(t, conn1, "shared\n")

	isSharedOutput := func(m protocol.Message) bool {
		return m.Type == protocol.TypeOutput && strings.Contains(string(m.Data), "shared")
	}

	if r1.waitFor(2*time.Second, isSharedOutput) == nil {
		t.Error("client 1 did not receive shared output")
	}
	if r2.waitFor(2*time.Second, isSharedOutput) == nil {
		t.Error("client 2 did not receive shared output")
	}

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/sessions/"+sess.ID, nil)
	http.DefaultClient.Do(req)
}

func TestIntegration_ProcessExitNotifiesClients(t *testing.T) {
	ts, _ := setupTestServer(t)

	sess := createSession(t, ts, []string{"echo", "done"}, "exit-test")
	conn := connectWS(t, ts, sess.ID)
	reader := newWSReader(t, conn)

	msg := reader.waitFor(3*time.Second, func(m protocol.Message) bool {
		return m.Type == protocol.TypeExit
	})
	if msg == nil {
		t.Fatal("did not receive exit message after process ended")
	}
	if msg.ExitCode == nil || *msg.ExitCode != 0 {
		t.Errorf("exit code = %v, want 0", msg.ExitCode)
	}

	time.Sleep(100 * time.Millisecond)
	resp, _ := http.Get(ts.URL + "/api/sessions")
	var sessions []sessionResponse
	json.NewDecoder(resp.Body).Decode(&sessions)
	resp.Body.Close()

	for _, s := range sessions {
		if s.ID == sess.ID {
			t.Error("session should be auto-removed after process exit")
		}
	}
}

func TestIntegration_ScrollbackReplay(t *testing.T) {
	ts, _ := setupTestServer(t)

	sess := createSession(t, ts, []string{"cat"}, "scrollback-test")

	// First client sends input that cat echoes
	conn1 := connectWS(t, ts, sess.ID)
	r1 := newWSReader(t, conn1)
	r1.collect(300 * time.Millisecond)
	sendInput(t, conn1, "first line\n")

	// Wait for echo to complete
	r1.waitFor(2*time.Second, func(m protocol.Message) bool {
		return m.Type == protocol.TypeOutput && strings.Contains(string(m.Data), "first line")
	})

	// Disconnect first client
	conn1.Close()
	time.Sleep(100 * time.Millisecond)

	// Second client connects and should get scrollback replay
	conn2 := connectWS(t, ts, sess.ID)
	r2 := newWSReader(t, conn2)

	msg := r2.waitFor(2*time.Second, func(m protocol.Message) bool {
		return m.Type == protocol.TypeOutput && strings.Contains(string(m.Data), "first line")
	})
	if msg == nil {
		t.Error("new client did not receive scrollback replay")
	}

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/sessions/"+sess.ID, nil)
	http.DefaultClient.Do(req)
}

func TestIntegration_InfoEndpoint(t *testing.T) {
	ts, _ := setupTestServer(t)

	resp, err := http.Get(ts.URL + "/api/info")
	if err != nil {
		t.Fatalf("GET /api/info failed: %v", err)
	}
	defer resp.Body.Close()

	var info map[string]string
	json.NewDecoder(resp.Body).Decode(&info)

	if info["hostname"] == "" {
		t.Error("hostname should not be empty")
	}
}

func TestIntegration_CreateSessionValidation(t *testing.T) {
	ts, _ := setupTestServer(t)

	// Missing command
	body, _ := json.Marshal(map[string]any{
		"name": "bad",
		"cols": 80,
		"rows": 24,
	})
	resp, _ := http.Post(ts.URL+"/api/sessions", "application/json", bytes.NewReader(body))
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("missing command: status = %d, want 400", resp.StatusCode)
	}
	resp.Body.Close()

	// Invalid JSON
	resp, _ = http.Post(ts.URL+"/api/sessions", "application/json", strings.NewReader("{bad"))
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("invalid JSON: status = %d, want 400", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestIntegration_WebSocketToNonexistentSession(t *testing.T) {
	ts, _ := setupTestServer(t)

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/session/nonexistent"
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		t.Error("expected WebSocket dial to fail for nonexistent session")
	}
	if resp != nil && resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestIntegration_DeleteNonexistentSession(t *testing.T) {
	ts, _ := setupTestServer(t)

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/sessions/nonexistent", nil)
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("DELETE nonexistent: status = %d, want 404", resp.StatusCode)
	}
	resp.Body.Close()
}
