package server

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ideamans/eternal/pkg/agent"
	"github.com/ideamans/eternal/pkg/protocol"
)

// WebDist is set from outside (cmd/et) to inject the embedded FS.
var WebDist embed.FS

// Version is set from outside (cmd/et) to inject the build version.
var Version string

// Peers is the list of peer server addresses for aggregation.
var Peers []string

// ExecPath is the path to the current binary, used to spawn agents.
var ExecPath string

type Server struct {
	upgrader   websocket.Upgrader
	hostname   string
	httpServer *http.Server
}

func New() *Server {
	hostname, _ := os.Hostname()
	return &Server{
		hostname: hostname,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// REST API
	mux.HandleFunc("GET /api/info", s.handleInfo)
	mux.HandleFunc("GET /api/sessions", s.handleListSessions)
	mux.HandleFunc("POST /api/sessions", s.handleCreateSession)
	mux.HandleFunc("GET /api/sessions/{id}", s.handleGetSession)
	mux.HandleFunc("DELETE /api/sessions/{id}", s.handleDeleteSession)
	mux.HandleFunc("GET /api/peers", s.handlePeers)

	// Peer proxy (REST)
	mux.HandleFunc("GET /api/peer/{index}/info", s.handlePeerProxy)
	mux.HandleFunc("GET /api/peer/{index}/sessions", s.handlePeerProxy)
	mux.HandleFunc("DELETE /api/peer/{index}/sessions/{id}", s.handlePeerProxy)

	// Peer proxy (WebSocket)
	mux.HandleFunc("GET /ws/peer/{index}/session/{id}", s.handlePeerWebSocketProxy)

	// WebSocket
	mux.HandleFunc("GET /ws/session/{id}", s.handleWebSocket)

	// Static files (Web UI)
	distFS, err := fs.Sub(WebDist, "dist")
	if err != nil {
		log.Fatalf("failed to load embedded web assets: %v", err)
	}
	fileServer := http.FileServer(http.FS(distFS))
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			f, err := distFS.(fs.ReadFileFS).ReadFile(r.URL.Path[1:])
			if err != nil {
				r.URL.Path = "/"
			} else {
				_ = f
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		fileServer.ServeHTTP(w, r)
	})

	return corsMiddleware(mux)
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"hostname": s.hostname,
		"version":  Version,
	})
}

func (s *Server) handlePeers(w http.ResponseWriter, r *http.Request) {
	peers := Peers
	if peers == nil {
		peers = []string{}
	}
	writeJSON(w, http.StatusOK, peers)
}

// ─── Session handlers (agent-backed) ─────────────────────────────────

// SessionInfo is the JSON response for session listing/detail.
type SessionInfo struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Command   []string  `json:"command"`
	Dir       string    `json:"dir"`
	Cols      int       `json:"cols"`
	Rows      int       `json:"rows"`
	Clients   int       `json:"clients"`
	CreatedAt time.Time `json:"created_at"`
	LastUsed  time.Time `json:"last_used"`
}

func metaToInfo(m *protocol.AgentMeta) SessionInfo {
	return SessionInfo{
		ID:        m.ID,
		Name:      m.Name,
		Command:   m.Command,
		Dir:       m.Dir,
		Cols:      m.Cols,
		Rows:      m.Rows,
		CreatedAt: m.CreatedAt,
		LastUsed:  m.CreatedAt, // approximate
	}
}

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	agents, err := agent.ListAgents()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	sessions := make([]SessionInfo, 0, len(agents))
	for _, a := range agents {
		sessions = append(sessions, metaToInfo(a))
	}
	writeJSON(w, http.StatusOK, sessions)
}

type createRequest struct {
	Name    string   `json:"name"`
	Command []string `json:"command"`
	Dir     string   `json:"dir"`
	Cols    int      `json:"cols"`
	Rows    int      `json:"rows"`
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	if len(req.Command) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "command is required"})
		return
	}

	id := generateID()

	// Spawn agent process
	args := []string{"agent", "--id", id}
	if req.Name != "" {
		args = append(args, "--name", req.Name)
	}
	if req.Dir != "" {
		args = append(args, "--dir", req.Dir)
	}
	if req.Cols > 0 {
		args = append(args, "--cols", strconv.Itoa(req.Cols))
	}
	if req.Rows > 0 {
		args = append(args, "--rows", strconv.Itoa(req.Rows))
	}
	args = append(args, "--")
	args = append(args, req.Command...)

	cmd := exec.Command(ExecPath, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil

	if err := cmd.Start(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("failed to spawn agent: %v", err)})
		return
	}

	// Don't wait for the agent — it runs independently
	go cmd.Wait()

	// Wait for the metadata file to appear
	var meta *protocol.AgentMeta
	for i := 0; i < 50; i++ { // 5 seconds max
		time.Sleep(100 * time.Millisecond)
		m, err := agent.ReadMeta(id)
		if err == nil {
			meta = m
			break
		}
	}

	if meta == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "agent did not start in time"})
		return
	}

	writeJSON(w, http.StatusCreated, metaToInfo(meta))
}

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	meta, err := agent.FindAgent(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	writeJSON(w, http.StatusOK, metaToInfo(meta))
}

func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	meta, err := agent.FindAgent(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}

	// Connect to agent and send kill
	conn, err := net.DialTimeout("unix", agent.SocketPath(meta.ID), 2*time.Second)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cannot connect to agent"})
		return
	}
	defer conn.Close()

	// Read hello (required by protocol)
	var hello protocol.AgentHello
	agent.ReadMsg(conn, &hello)

	// Send kill
	agent.WriteMsg(conn, protocol.Message{Type: protocol.TypeKill})
	conn.Close()

	writeJSON(w, http.StatusOK, map[string]string{"status": "killed"})
}

// ─── WebSocket relay (agent-backed) ──────────────────────────────────

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	meta, err := agent.FindAgent(id)
	if err != nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	// Connect to agent
	agentConn, err := net.DialTimeout("unix", agent.SocketPath(meta.ID), 2*time.Second)
	if err != nil {
		http.Error(w, "agent unreachable", http.StatusBadGateway)
		return
	}
	defer agentConn.Close()

	// Read hello
	var hello protocol.AgentHello
	if err := agent.ReadMsg(agentConn, &hello); err != nil {
		http.Error(w, "agent protocol error", http.StatusBadGateway)
		return
	}

	if !hello.Alive {
		http.Error(w, "session is dead", http.StatusGone)
		return
	}

	// Upgrade WebSocket
	wsConn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("websocket upgrade failed: %v", err)
		return
	}
	defer wsConn.Close()

	// Send initial resize
	resizeMsg := protocol.Message{Type: protocol.TypeResize, Cols: hello.Cols, Rows: hello.Rows}
	if data, err := json.Marshal(resizeMsg); err == nil {
		wsConn.WriteMessage(websocket.TextMessage, data)
	}

	// Send scrollback
	if len(hello.Scrollback) > 0 {
		scrollMsg := protocol.Message{Type: protocol.TypeOutput, Data: hello.Scrollback}
		if data, err := json.Marshal(scrollMsg); err == nil {
			wsConn.WriteMessage(websocket.TextMessage, data)
		}
	}

	done := make(chan struct{})

	// Agent → WebSocket
	go func() {
		defer close(done)
		for {
			var msg protocol.Message
			if err := agent.ReadMsg(agentConn, &msg); err != nil {
				return
			}
			data, err := json.Marshal(msg)
			if err != nil {
				continue
			}
			if err := wsConn.WriteMessage(websocket.TextMessage, data); err != nil {
				return
			}
		}
	}()

	// WebSocket → Agent
	go func() {
		for {
			_, raw, err := wsConn.ReadMessage()
			if err != nil {
				agentConn.Close()
				return
			}
			var msg protocol.Message
			if err := json.Unmarshal(raw, &msg); err != nil {
				continue
			}
			if err := agent.WriteMsg(agentConn, msg); err != nil {
				return
			}
		}
	}()

	<-done
}

// ─── Peer proxy ──────────────────────────────────────────────────────

func getPeerURL(r *http.Request) (string, error) {
	idx, err := strconv.Atoi(r.PathValue("index"))
	if err != nil || idx < 0 || idx >= len(Peers) {
		return "", fmt.Errorf("invalid peer index")
	}
	return Peers[idx], nil
}

func (s *Server) handlePeerProxy(w http.ResponseWriter, r *http.Request) {
	peerURL, err := getPeerURL(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	prefix := fmt.Sprintf("/api/peer/%s", r.PathValue("index"))
	upstreamPath := strings.TrimPrefix(r.URL.Path, prefix)
	targetURL := peerURL + "/api" + upstreamPath

	req, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, r.Body)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	req.Header.Set("Content-Type", r.Header.Get("Content-Type"))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": fmt.Sprintf("peer unreachable: %v", err)})
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func (s *Server) handlePeerWebSocketProxy(w http.ResponseWriter, r *http.Request) {
	peerURL, err := getPeerURL(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	sessionID := r.PathValue("id")
	wsScheme := "ws"
	if strings.HasPrefix(peerURL, "https://") {
		wsScheme = "wss"
	}
	host := strings.TrimPrefix(strings.TrimPrefix(peerURL, "http://"), "https://")
	upstreamURL := fmt.Sprintf("%s://%s/ws/session/%s", wsScheme, host, sessionID)

	upstreamConn, _, err := websocket.DefaultDialer.Dial(upstreamURL, nil)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to connect to peer: %v", err), http.StatusBadGateway)
		return
	}
	defer upstreamConn.Close()

	clientConn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("websocket upgrade failed: %v", err)
		return
	}
	defer clientConn.Close()

	done := make(chan struct{})

	go func() {
		defer close(done)
		for {
			msgType, data, err := upstreamConn.ReadMessage()
			if err != nil {
				return
			}
			if err := clientConn.WriteMessage(msgType, data); err != nil {
				return
			}
		}
	}()

	go func() {
		for {
			msgType, data, err := clientConn.ReadMessage()
			if err != nil {
				upstreamConn.Close()
				return
			}
			if err := upstreamConn.WriteMessage(msgType, data); err != nil {
				return
			}
		}
	}()

	<-done
}

// ─── Server lifecycle ────────────────────────────────────────────────

func (s *Server) ListenAndServe(addr string) error {
	parts := strings.SplitN(addr, ":", 2)
	host := "0.0.0.0"
	port := "2840"
	if len(parts) == 2 {
		if parts[0] != "" {
			host = parts[0]
		}
		port = parts[1]
	} else if len(parts) == 1 {
		port = parts[0]
	}
	listenAddr := fmt.Sprintf("%s:%s", host, port)
	log.Printf("eternal server listening on %s", listenAddr)

	// Clean stale agents on startup
	agent.CleanStale()

	s.httpServer = &http.Server{
		Addr:    listenAddr,
		Handler: s.Handler(),
	}
	return s.httpServer.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpServer != nil {
		return s.httpServer.Shutdown(ctx)
	}
	return nil
}

func generateID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}
