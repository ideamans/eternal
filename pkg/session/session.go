package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/ideamans/eternal/pkg/protocol"
)

type Session struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Command   []string  `json:"command"`
	Dir       string    `json:"dir"`
	Cols      int       `json:"cols"`
	Rows      int       `json:"rows"`
	Clients   int       `json:"clients"`
	CreatedAt time.Time `json:"created_at"`
	LastUsed  time.Time `json:"last_used"`

	ptyDev     PTY
	process    Process
	exitCode   *int
	clients    map[string]*clientEntry
	scrollback []byte
	mu         sync.Mutex
	onExit     func(s *Session)
}

type clientEntry struct {
	ID   string
	Conn ClientConn
}

type CreateOptions struct {
	ID         string
	Name       string
	Command    []string
	Dir        string
	Cols       int
	Rows       int
	OnExit     func(s *Session)
	PTYFactory PTYFactory
	OSEnv      OSEnv
}

func New(opts CreateOptions) (*Session, error) {
	if len(opts.Command) == 0 {
		return nil, errors.New("command is required")
	}

	factory := opts.PTYFactory
	if factory == nil {
		factory = &RealPTYFactory{}
	}
	osEnv := opts.OSEnv
	if osEnv == nil {
		osEnv = &RealOSEnv{}
	}

	cols := opts.Cols
	if cols == 0 {
		cols = 80
	}
	rows := opts.Rows
	if rows == 0 {
		rows = 24
	}

	dir := opts.Dir
	if dir != "" {
		if info, err := osEnv.Stat(dir); err != nil || !info.IsDir() {
			dir = ""
		}
	}

	p, proc, err := factory.Start(opts.Command, dir, osEnv.Environ(), cols, rows)
	if err != nil {
		return nil, fmt.Errorf("failed to start pty: %w", err)
	}

	s := &Session{
		ID:        opts.ID,
		Name:      opts.Name,
		Command:   opts.Command,
		Dir:       opts.Dir,
		Cols:      cols,
		Rows:      rows,
		CreatedAt: time.Now(),
		LastUsed:  time.Now(),
		ptyDev:    p,
		process:   proc,
		clients:   make(map[string]*clientEntry),
		onExit:    opts.OnExit,
	}

	go s.readLoop()
	go s.waitProcess()

	return s, nil
}

// readLoop reads PTY output and broadcasts to all connected clients.
func (s *Session) readLoop() {
	buf := make([]byte, 4096)
	for {
		n, err := s.ptyDev.Read(buf)
		if err != nil {
			if err != io.EOF {
				// PTY closed
			}
			return
		}
		data := make([]byte, n)
		copy(data, buf[:n])
		s.broadcast(protocol.Message{
			Type: protocol.TypeOutput,
			Data: data,
		})
	}
}

// waitProcess waits for the command to exit and cleans up.
func (s *Session) waitProcess() {
	exitCode, _ := s.process.Wait()

	s.mu.Lock()
	s.exitCode = &exitCode
	s.mu.Unlock()

	s.broadcast(protocol.Message{
		Type:     protocol.TypeExit,
		ExitCode: &exitCode,
	})
	s.CloseAllClients()

	if s.onExit != nil {
		s.onExit(s)
	}
}

const maxScrollback = 64 * 1024 // 64KB of recent output

func (s *Session) broadcast(msg protocol.Message) {
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if msg.Type == protocol.TypeOutput && len(msg.Data) > 0 {
		s.scrollback = append(s.scrollback, msg.Data...)
		if len(s.scrollback) > maxScrollback {
			s.scrollback = s.scrollback[len(s.scrollback)-maxScrollback:]
		}
	}

	for id, c := range s.clients {
		if err := c.Conn.WriteMessage(data); err != nil {
			c.Conn.Close()
			delete(s.clients, id)
		}
	}
	s.Clients = len(s.clients)
}

func (s *Session) AddClient(id string, conn ClientConn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clients[id] = &clientEntry{ID: id, Conn: conn}
	s.Clients = len(s.clients)
	s.LastUsed = time.Now()

	// Send current terminal size so browser can match
	resizeMsg := protocol.Message{
		Type: protocol.TypeResize,
		Cols: s.Cols,
		Rows: s.Rows,
	}
	if data, err := json.Marshal(resizeMsg); err == nil {
		conn.WriteMessage(data)
	}

	// Replay scrollback buffer to the new client
	if len(s.scrollback) > 0 {
		msg := protocol.Message{
			Type: protocol.TypeOutput,
			Data: s.scrollback,
		}
		if data, err := json.Marshal(msg); err == nil {
			conn.WriteMessage(data)
		}
	}
}

func (s *Session) RemoveClient(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.clients[id]; ok {
		c.Conn.Close()
		delete(s.clients, id)
	}
	s.Clients = len(s.clients)
}

func (s *Session) CloseAllClients() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, c := range s.clients {
		c.Conn.Close()
		delete(s.clients, id)
	}
	s.Clients = 0
}

func (s *Session) WriteInput(data []byte) error {
	s.mu.Lock()
	s.LastUsed = time.Now()
	s.mu.Unlock()
	_, err := s.ptyDev.Write(data)
	return err
}

func (s *Session) Resize(cols, rows int) error {
	s.mu.Lock()
	s.Cols = cols
	s.Rows = rows
	s.mu.Unlock()

	err := s.ptyDev.Resize(cols, rows)

	s.broadcast(protocol.Message{
		Type: protocol.TypeResize,
		Cols: cols,
		Rows: rows,
	})

	return err
}

// Kill sends SIGKILL to the process.
func (s *Session) Kill() error {
	return s.process.Signal(os.Kill)
}

func (s *Session) IsAlive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exitCode == nil
}

func (s *Session) Close() {
	s.ptyDev.Close()
}

// Scrollback returns a copy of the current scrollback buffer (for testing).
func (s *Session) Scrollback() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	buf := make([]byte, len(s.scrollback))
	copy(buf, s.scrollback)
	return buf
}

// ExitCode returns the exit code if the process has exited, or nil.
func (s *Session) ExitCode() *int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exitCode
}
