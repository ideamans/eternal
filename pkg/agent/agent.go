package agent

import (
	"encoding/json"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/ideamans/eternal/pkg/protocol"
	"github.com/ideamans/eternal/pkg/session"
)

// Options for starting an agent process.
type Options struct {
	ID      string
	Name    string
	Command []string
	Dir     string
	Cols    int
	Rows    int
}

// Run starts the agent: creates a PTY session, listens on a Unix socket,
// and relays I/O until the child process exits.
func Run(opts Options) error {
	if err := EnsureSocketDir(); err != nil {
		return err
	}

	sockPath := SocketPath(opts.ID)
	// Clean up stale socket if exists
	os.Remove(sockPath)

	sess, err := session.New(session.CreateOptions{
		ID:      opts.ID,
		Name:    opts.Name,
		Command: opts.Command,
		Dir:     opts.Dir,
		Cols:    opts.Cols,
		Rows:    opts.Rows,
	})
	if err != nil {
		return err
	}

	// Write metadata file
	meta := &protocol.AgentMeta{
		ID:        sess.ID,
		Name:      sess.Name,
		Command:   sess.Command,
		Dir:       sess.Dir,
		Cols:      sess.Cols,
		Rows:      sess.Rows,
		CreatedAt: sess.CreatedAt,
		PID:       os.Getpid(),
	}
	if err := WriteMeta(meta); err != nil {
		sess.Kill()
		return err
	}

	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		sess.Kill()
		os.Remove(MetaPath(opts.ID))
		return err
	}
	defer listener.Close()
	os.Chmod(sockPath, 0600)

	// Track connected clients for broadcasting
	a := &agentServer{
		sess:     sess,
		listener: listener,
		sockPath: sockPath,
		metaPath: MetaPath(opts.ID),
	}

	// Register the agent as a "client" of the session to receive broadcasts
	a.startBroadcastRelay()

	// Handle signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		sess.Kill()
	}()

	// Wait for session to die in background, then shut down listener
	go func() {
		for sess.IsAlive() {
			time.Sleep(100 * time.Millisecond)
		}
		listener.Close()
	}()

	// Accept connections
	for {
		conn, err := listener.Accept()
		if err != nil {
			// Listener closed (session exited)
			break
		}
		go a.handleConn(conn)
	}

	// Cleanup
	a.cleanup()
	return nil
}

type agentServer struct {
	sess     *session.Session
	listener net.Listener
	sockPath string
	metaPath string

	mu    sync.Mutex
	conns map[net.Conn]struct{}
}

// startBroadcastRelay registers a virtual client with the session
// that relays output to all connected Unix socket clients.
func (a *agentServer) startBroadcastRelay() {
	a.conns = make(map[net.Conn]struct{})
	relay := &broadcastRelay{agent: a}
	a.sess.AddClient("__agent_relay__", relay)
}

func (a *agentServer) handleConn(conn net.Conn) {
	defer conn.Close()

	// Send hello with session info + scrollback
	hello := protocol.AgentHello{
		Type:       protocol.TypeHello,
		ID:         a.sess.ID,
		Name:       a.sess.Name,
		Command:    a.sess.Command,
		Dir:        a.sess.Dir,
		Cols:       a.sess.Cols,
		Rows:       a.sess.Rows,
		CreatedAt:  a.sess.CreatedAt,
		Scrollback: a.sess.Scrollback(),
		Alive:      a.sess.IsAlive(),
		ExitCode:   a.sess.ExitCode(),
	}
	if err := WriteMsg(conn, hello); err != nil {
		return
	}

	// Register connection for broadcasts
	a.mu.Lock()
	a.conns[conn] = struct{}{}
	a.mu.Unlock()

	defer func() {
		a.mu.Lock()
		delete(a.conns, conn)
		a.mu.Unlock()
	}()

	// Read messages from client
	for {
		var msg protocol.Message
		if err := ReadMsg(conn, &msg); err != nil {
			return
		}
		switch msg.Type {
		case protocol.TypeInput:
			a.sess.WriteInput(msg.Data)
		case protocol.TypeResize:
			a.sess.Resize(msg.Cols, msg.Rows)
		case protocol.TypeKill:
			a.sess.Kill()
		}
	}
}

func (a *agentServer) cleanup() {
	a.sess.RemoveClient("__agent_relay__")
	a.sess.Close()
	os.Remove(a.sockPath)
	os.Remove(a.metaPath)
}

// broadcastRelay implements session.ClientConn and relays messages
// to all connected Unix socket clients.
type broadcastRelay struct {
	agent *agentServer
}

func (r *broadcastRelay) WriteMessage(data []byte) error {
	// Parse the JSON message to re-encode as length-prefixed
	var msg protocol.Message
	if err := json.Unmarshal(data, &msg); err != nil {
		return err
	}

	r.agent.mu.Lock()
	conns := make([]net.Conn, 0, len(r.agent.conns))
	for c := range r.agent.conns {
		conns = append(conns, c)
	}
	r.agent.mu.Unlock()

	for _, conn := range conns {
		if err := WriteMsg(conn, msg); err != nil {
			r.agent.mu.Lock()
			delete(r.agent.conns, conn)
			r.agent.mu.Unlock()
			conn.Close()
		}
	}
	return nil
}

func (r *broadcastRelay) Close() error {
	return nil
}
