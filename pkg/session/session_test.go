package session

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ideamans/eternal/pkg/protocol"
)

func TestScrollbackAccumulation(t *testing.T) {
	s, fp, proc := newTestSession()
	defer func() { fp.Close(); close(proc.done) }()

	client := newFakeClientConn()
	s.AddClient("c1", client)

	// Send output through the fake PTY
	fp.output <- []byte("hello ")
	fp.output <- []byte("world")

	waitUntil(t, time.Second, func() bool {
		return string(s.Scrollback()) == "hello world"
	})

	scrollback := s.Scrollback()
	if string(scrollback) != "hello world" {
		t.Errorf("scrollback = %q, want %q", scrollback, "hello world")
	}
}

func TestScrollbackTrimming(t *testing.T) {
	s, fp, proc := newTestSession()
	defer func() { fp.Close(); close(proc.done) }()

	// Write more than maxScrollback
	big := strings.Repeat("x", maxScrollback+1000)
	fp.output <- []byte(big)

	waitUntil(t, time.Second, func() bool {
		return len(s.Scrollback()) > 0
	})

	scrollback := s.Scrollback()
	if len(scrollback) > maxScrollback {
		t.Errorf("scrollback len = %d, want <= %d", len(scrollback), maxScrollback)
	}
}

func TestScrollbackReplayOnConnect(t *testing.T) {
	s, fp, proc := newTestSession()
	defer func() { fp.Close(); close(proc.done) }()

	// Send some output before any client connects
	fp.output <- []byte("existing output")
	waitUntil(t, time.Second, func() bool {
		return len(s.Scrollback()) > 0
	})

	// Now connect a new client
	client := newFakeClientConn()
	s.AddClient("late", client)

	messages := client.getMessages()
	// Should have received: resize message + scrollback replay
	if len(messages) < 2 {
		t.Fatalf("expected at least 2 messages (resize + scrollback), got %d", len(messages))
	}

	// The last message should contain the scrollback
	var msg protocol.Message
	json.Unmarshal(messages[len(messages)-1], &msg)
	if msg.Type != protocol.TypeOutput {
		t.Errorf("replay message type = %q, want %q", msg.Type, protocol.TypeOutput)
	}
	if string(msg.Data) != "existing output" {
		t.Errorf("replay data = %q, want %q", msg.Data, "existing output")
	}
}

func TestBroadcastToMultipleClients(t *testing.T) {
	s, fp, proc := newTestSession()
	defer func() { fp.Close(); close(proc.done) }()

	c1 := newFakeClientConn()
	c2 := newFakeClientConn()
	s.AddClient("c1", c1)
	s.AddClient("c2", c2)

	fp.output <- []byte("broadcast")

	for _, c := range []*fakeClientConn{c1, c2} {
		waitUntil(t, time.Second, func() bool {
			for _, raw := range c.getMessages() {
				var msg protocol.Message
				json.Unmarshal(raw, &msg)
				if msg.Type == protocol.TypeOutput && string(msg.Data) == "broadcast" {
					return true
				}
			}
			return false
		})
	}
}

func TestBroadcastRemovesBrokenClient(t *testing.T) {
	s, fp, proc := newTestSession()
	defer func() { fp.Close(); close(proc.done) }()

	good := newFakeClientConn()
	bad := newFakeClientConn()
	bad.writeErr = os.ErrClosed // simulate broken connection

	s.AddClient("good", good)
	s.AddClient("bad", bad)

	if s.Clients != 2 {
		t.Fatalf("clients = %d, want 2", s.Clients)
	}

	fp.output <- []byte("test")

	waitUntil(t, time.Second, func() bool {
		return s.Clients == 1
	})

	if !bad.isClosed() {
		t.Error("broken client should have been closed")
	}
}

func TestProcessExit(t *testing.T) {
	exitCalled := make(chan string, 1)

	s, fp, proc := newTestSession(func(o *CreateOptions) {
		o.OnExit = func(s *Session) {
			exitCalled <- s.ID
		}
	})
	_ = fp

	client := newFakeClientConn()
	s.AddClient("c1", client)

	// Trigger process exit with code 42
	proc.exitCode = 42
	close(proc.done)

	select {
	case id := <-exitCalled:
		if id != "test-id" {
			t.Errorf("onExit session ID = %q, want %q", id, "test-id")
		}
	case <-time.After(time.Second):
		t.Fatal("onExit was not called")
	}

	// Check exit code
	code := s.ExitCode()
	if code == nil || *code != 42 {
		t.Errorf("exit code = %v, want 42", code)
	}

	// Client should have received exit message
	waitUntil(t, time.Second, func() bool {
		for _, raw := range client.getMessages() {
			var msg protocol.Message
			json.Unmarshal(raw, &msg)
			if msg.Type == protocol.TypeExit {
				return true
			}
		}
		return false
	})

	found := false
	for _, raw := range client.getMessages() {
		var msg protocol.Message
		json.Unmarshal(raw, &msg)
		if msg.Type == protocol.TypeExit && msg.ExitCode != nil && *msg.ExitCode == 42 {
			found = true
			break
		}
	}
	if !found {
		t.Error("client did not receive exit message with code 42")
	}

	if s.IsAlive() {
		t.Error("session should not be alive after process exit")
	}
}

func TestWriteInput(t *testing.T) {
	s, fp, proc := newTestSession()
	defer func() { fp.Close(); close(proc.done) }()

	before := s.LastUsed

	time.Sleep(time.Millisecond)
	s.WriteInput([]byte("hello"))

	input := fp.getInput()
	if string(input) != "hello" {
		t.Errorf("PTY input = %q, want %q", input, "hello")
	}

	if !s.LastUsed.After(before) {
		t.Error("LastUsed should be updated after WriteInput")
	}
}

func TestResize(t *testing.T) {
	s, fp, proc := newTestSession()
	defer func() { fp.Close(); close(proc.done) }()

	client := newFakeClientConn()
	s.AddClient("c1", client)

	// Clear initial messages (resize + possible scrollback from AddClient)
	client.drainMessages()

	s.Resize(120, 40)

	// Check PTY received resize
	resizes := fp.getResizes()
	found := false
	for _, r := range resizes {
		if r.Cols == 120 && r.Rows == 40 {
			found = true
			break
		}
	}
	if !found {
		t.Error("PTY did not receive resize(120, 40)")
	}

	// Check session dimensions updated
	if s.Cols != 120 || s.Rows != 40 {
		t.Errorf("session size = %dx%d, want 120x40", s.Cols, s.Rows)
	}

	// Check client received resize broadcast (Resize -> broadcast is synchronous)
	msgs := client.getMessages()
	resizeBroadcast := false
	for _, raw := range msgs {
		var msg protocol.Message
		json.Unmarshal(raw, &msg)
		if msg.Type == protocol.TypeResize && msg.Cols == 120 && msg.Rows == 40 {
			resizeBroadcast = true
			break
		}
	}
	if !resizeBroadcast {
		t.Error("client did not receive resize broadcast")
	}
}

func TestKill(t *testing.T) {
	s, _, proc := newTestSession()
	defer close(proc.done)

	s.Kill()

	signals := proc.getSignals()
	if len(signals) == 0 {
		t.Fatal("no signal sent to process")
	}
	if signals[0] != os.Kill {
		t.Errorf("signal = %v, want SIGKILL", signals[0])
	}
}

func TestAddRemoveClient(t *testing.T) {
	s, fp, proc := newTestSession()
	defer func() { fp.Close(); close(proc.done) }()

	c1 := newFakeClientConn()
	c2 := newFakeClientConn()

	s.AddClient("c1", c1)
	if s.Clients != 1 {
		t.Errorf("clients = %d, want 1", s.Clients)
	}

	s.AddClient("c2", c2)
	if s.Clients != 2 {
		t.Errorf("clients = %d, want 2", s.Clients)
	}

	s.RemoveClient("c1")
	if s.Clients != 1 {
		t.Errorf("clients = %d, want 1", s.Clients)
	}

	s.CloseAllClients()
	if s.Clients != 0 {
		t.Errorf("clients = %d, want 0", s.Clients)
	}
}

func TestNewSessionDefaults(t *testing.T) {
	s, fp, proc := newTestSession(func(o *CreateOptions) {
		o.Cols = 0
		o.Rows = 0
	})
	defer func() { fp.Close(); close(proc.done) }()

	if s.Cols != 80 {
		t.Errorf("cols = %d, want 80", s.Cols)
	}
	if s.Rows != 24 {
		t.Errorf("rows = %d, want 24", s.Rows)
	}
}

func TestNewSessionRequiresCommand(t *testing.T) {
	_, err := New(CreateOptions{
		ID:         "x",
		Command:    nil,
		PTYFactory: &fakePTYFactory{pty: newFakePTY(), process: newFakeProcess(0)},
	})
	if err == nil {
		t.Error("expected error for empty command")
	}
}

func TestResizeBroadcastOnAddClient(t *testing.T) {
	s, fp, proc := newTestSession(func(o *CreateOptions) {
		o.Cols = 100
		o.Rows = 50
	})
	defer func() { fp.Close(); close(proc.done) }()

	client := newFakeClientConn()
	s.AddClient("c1", client)

	messages := client.getMessages()
	if len(messages) == 0 {
		t.Fatal("no messages received on AddClient")
	}

	var msg protocol.Message
	json.Unmarshal(messages[0], &msg)
	if msg.Type != protocol.TypeResize {
		t.Errorf("first message type = %q, want %q", msg.Type, protocol.TypeResize)
	}
	if msg.Cols != 100 || msg.Rows != 50 {
		t.Errorf("resize = %dx%d, want 100x50", msg.Cols, msg.Rows)
	}
}

func TestNewSessionPTYStartFailure(t *testing.T) {
	_, err := New(CreateOptions{
		ID:         "fail",
		Command:    []string{"cmd"},
		PTYFactory: &errPTYFactory{err: errors.New("pty start failed")},
		OSEnv:      &fakeOSEnv{env: []string{}, isDir: true},
	})
	if err == nil {
		t.Fatal("expected error when PTY Start fails")
	}
	if !strings.Contains(err.Error(), "failed to start pty") {
		t.Errorf("error = %q, want it to contain 'failed to start pty'", err)
	}
}

func TestAddClientDuplicateID(t *testing.T) {
	s, fp, proc := newTestSession()
	defer func() { fp.Close(); close(proc.done) }()

	c1 := newFakeClientConn()
	c2 := newFakeClientConn()

	s.AddClient("same-id", c1)
	s.AddClient("same-id", c2) // should overwrite

	if s.Clients != 1 {
		t.Errorf("clients = %d, want 1 (duplicate ID should replace)", s.Clients)
	}
}

func TestWriteInputAfterExit(t *testing.T) {
	s, fp, proc := newTestSession()

	// Exit the process
	proc.exitCode = 0
	close(proc.done)

	waitUntil(t, time.Second, func() bool {
		return !s.IsAlive()
	})

	fp.Close()
	// Writing to a closed PTY should not panic
	err := s.WriteInput([]byte("hello"))
	_ = err
}

func TestResizeAfterExit(t *testing.T) {
	s, fp, proc := newTestSession()

	proc.exitCode = 0
	close(proc.done)

	waitUntil(t, time.Second, func() bool {
		return !s.IsAlive()
	})

	fp.Close()
	// Resize on a closed PTY should not panic
	err := s.Resize(120, 40)
	_ = err
}
