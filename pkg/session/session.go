package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
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

	cmd        *exec.Cmd
	ptmx       *os.File
	exitCode   *int
	clients    map[string]*Client
	scrollback []byte // ring buffer of recent output for replay
	mu         sync.Mutex
	onExit     func(s *Session)
}

type Client struct {
	ID   string
	Conn *websocket.Conn
}

type CreateOptions struct {
	ID      string
	Name    string
	Command []string
	Dir     string
	Cols    int
	Rows    int
	OnExit  func(s *Session)
}

func New(opts CreateOptions) (*Session, error) {
	if len(opts.Command) == 0 {
		return nil, errors.New("command is required")
	}

	cmd := exec.Command(opts.Command[0], opts.Command[1:]...)
	cmd.Env = os.Environ()
	if opts.Dir != "" {
		if info, err := os.Stat(opts.Dir); err == nil && info.IsDir() {
			cmd.Dir = opts.Dir
		}
	}

	cols := opts.Cols
	if cols == 0 {
		cols = 80
	}
	rows := opts.Rows
	if rows == 0 {
		rows = 24
	}

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{
		Cols: uint16(cols),
		Rows: uint16(rows),
	})
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
		cmd:       cmd,
		ptmx:      ptmx,
		clients:   make(map[string]*Client),
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
		n, err := s.ptmx.Read(buf)
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
	exitCode := 0
	if err := s.cmd.Wait(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
	}

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

	// Append to scrollback buffer for replay on new client connect
	if msg.Type == protocol.TypeOutput && len(msg.Data) > 0 {
		s.scrollback = append(s.scrollback, msg.Data...)
		if len(s.scrollback) > maxScrollback {
			s.scrollback = s.scrollback[len(s.scrollback)-maxScrollback:]
		}
	}

	for id, c := range s.clients {
		if err := c.Conn.WriteMessage(websocket.TextMessage, data); err != nil {
			c.Conn.Close()
			delete(s.clients, id)
		}
	}
	s.Clients = len(s.clients)
}

func (s *Session) AddClient(id string, conn *websocket.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clients[id] = &Client{ID: id, Conn: conn}
	s.Clients = len(s.clients)
	s.LastUsed = time.Now()

	// Send current terminal size so browser can match
	resizeMsg := protocol.Message{
		Type: protocol.TypeResize,
		Cols: s.Cols,
		Rows: s.Rows,
	}
	if data, err := json.Marshal(resizeMsg); err == nil {
		conn.WriteMessage(websocket.TextMessage, data)
	}

	// Replay scrollback buffer to the new client
	if len(s.scrollback) > 0 {
		msg := protocol.Message{
			Type: protocol.TypeOutput,
			Data: s.scrollback,
		}
		if data, err := json.Marshal(msg); err == nil {
			conn.WriteMessage(websocket.TextMessage, data)
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
	_, err := s.ptmx.Write(data)
	return err
}

func (s *Session) Resize(cols, rows int) error {
	s.mu.Lock()
	s.Cols = cols
	s.Rows = rows
	s.mu.Unlock()

	err := pty.Setsize(s.ptmx, &pty.Winsize{
		Cols: uint16(cols),
		Rows: uint16(rows),
	})

	// Broadcast new size to all clients (browser viewers follow this)
	s.broadcast(protocol.Message{
		Type: protocol.TypeResize,
		Cols: cols,
		Rows: rows,
	})

	return err
}

// Kill sends SIGTERM to the process.
func (s *Session) Kill() error {
	if s.cmd.Process == nil {
		return nil
	}
	return s.cmd.Process.Signal(os.Kill)
}

func (s *Session) IsAlive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exitCode == nil
}

func (s *Session) Close() {
	s.ptmx.Close()
}
