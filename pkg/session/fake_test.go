package session

import (
	"io"
	"os"
	"sync"
	"testing"
	"time"
)

// fakePTY is a controllable PTY for testing.
type fakePTY struct {
	mu      sync.Mutex
	input   []byte   // written via Write (captures input)
	output  chan []byte // data to return from Read
	resizes []struct{ Cols, Rows int }
	closed  bool
}

func newFakePTY() *fakePTY {
	return &fakePTY{
		output: make(chan []byte, 100),
	}
}

func (f *fakePTY) Read(p []byte) (int, error) {
	data, ok := <-f.output
	if !ok {
		return 0, io.EOF
	}
	n := copy(p, data)
	return n, nil
}

func (f *fakePTY) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.input = append(f.input, p...)
	return len(p), nil
}

func (f *fakePTY) Resize(cols, rows int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resizes = append(f.resizes, struct{ Cols, Rows int }{cols, rows})
	return nil
}

func (f *fakePTY) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.closed {
		f.closed = true
		close(f.output)
	}
	return nil
}

func (f *fakePTY) getInput() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]byte, len(f.input))
	copy(out, f.input)
	return out
}

func (f *fakePTY) getResizes() []struct{ Cols, Rows int } {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]struct{ Cols, Rows int }, len(f.resizes))
	copy(out, f.resizes)
	return out
}

// fakeProcess is a controllable process for testing.
type fakeProcess struct {
	exitCode int
	exitErr  error
	done     chan struct{} // close to trigger Wait() return
	signals  []os.Signal
	mu       sync.Mutex
}

func newFakeProcess(exitCode int) *fakeProcess {
	return &fakeProcess{
		exitCode: exitCode,
		done:     make(chan struct{}),
	}
}

func (f *fakeProcess) Wait() (int, error) {
	<-f.done
	return f.exitCode, f.exitErr
}

func (f *fakeProcess) Signal(sig os.Signal) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.signals = append(f.signals, sig)
	return nil
}

func (f *fakeProcess) getSignals() []os.Signal {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]os.Signal, len(f.signals))
	copy(out, f.signals)
	return out
}

// fakePTYFactory returns pre-configured fakes.
type fakePTYFactory struct {
	pty     *fakePTY
	process *fakeProcess
}

func (f *fakePTYFactory) Start(command []string, dir string, env []string, cols, rows int) (PTY, Process, error) {
	return f.pty, f.process, nil
}

// errPTYFactory always returns an error from Start.
type errPTYFactory struct {
	err error
}

func (f *errPTYFactory) Start(command []string, dir string, env []string, cols, rows int) (PTY, Process, error) {
	return nil, nil, f.err
}

// fakeClientConn captures messages sent to a client.
type fakeClientConn struct {
	mu       sync.Mutex
	messages [][]byte
	closed   bool
	writeErr error // if set, WriteMessage returns this error
}

func newFakeClientConn() *fakeClientConn {
	return &fakeClientConn{}
}

func (f *fakeClientConn) WriteMessage(data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writeErr != nil {
		return f.writeErr
	}
	msg := make([]byte, len(data))
	copy(msg, data)
	f.messages = append(f.messages, msg)
	return nil
}

func (f *fakeClientConn) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func (f *fakeClientConn) getMessages() [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([][]byte, len(f.messages))
	copy(out, f.messages)
	return out
}

// drainMessages returns all messages and clears the buffer.
func (f *fakeClientConn) drainMessages() [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.messages
	f.messages = nil
	return out
}

func (f *fakeClientConn) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

// fakeOSEnv returns canned environment values.
type fakeOSEnv struct {
	env     []string
	statErr error
	isDir   bool
}

func (f *fakeOSEnv) Environ() []string { return f.env }
func (f *fakeOSEnv) Stat(path string) (os.FileInfo, error) {
	if f.statErr != nil {
		return nil, f.statErr
	}
	return &fakeFileInfo{dir: f.isDir}, nil
}

type fakeFileInfo struct {
	dir bool
	os.FileInfo // embed to satisfy interface
}

func (f *fakeFileInfo) IsDir() bool { return f.dir }

// helper to create a session with fakes for testing.
func newTestSession(opts ...func(*CreateOptions)) (*Session, *fakePTY, *fakeProcess) {
	fp := newFakePTY()
	proc := newFakeProcess(0)

	o := CreateOptions{
		ID:      "test-id",
		Name:    "test",
		Command: []string{"fake-cmd"},
		Cols:    80,
		Rows:    24,
		PTYFactory: &fakePTYFactory{pty: fp, process: proc},
		OSEnv:     &fakeOSEnv{env: []string{"TERM=xterm"}, isDir: true},
	}
	for _, fn := range opts {
		fn(&o)
	}

	s, err := New(o)
	if err != nil {
		panic(err)
	}
	return s, fp, proc
}

// waitUntil polls fn every 5ms until it returns true, or fails after timeout.
func waitUntil(t *testing.T, timeout time.Duration, fn func() bool) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		if fn() {
			return
		}
		select {
		case <-deadline:
			t.Fatal("waitUntil timed out")
		case <-time.After(5 * time.Millisecond):
		}
	}
}
