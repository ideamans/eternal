package server

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/gorilla/websocket"
	"github.com/ideamans/eternal/pkg/protocol"
	"github.com/ideamans/eternal/pkg/session"
)

// WebDist is set from outside (cmd/et) to inject the embedded FS.
var WebDist embed.FS

// Version is set from outside (cmd/et) to inject the build version.
var Version string

// Peers is the list of peer server addresses for aggregation.
var Peers []string

type Server struct {
	manager  *session.Manager
	upgrader websocket.Upgrader
	hostname string
}

func New() *Server {
	hostname, _ := os.Hostname()
	return &Server{
		manager:  session.NewManager(),
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

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	sessions := s.manager.List()
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

	sess, err := s.manager.Create(session.CreateOptions{
		Name:    req.Name,
		Command: req.Command,
		Dir:     req.Dir,
		Cols:    req.Cols,
		Rows:    req.Rows,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusCreated, sess)
}

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess := s.manager.Get(id)
	if sess == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.manager.KillAndRemove(id); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "killed"})
}

// wsClientConn adapts *websocket.Conn to session.ClientConn.
type wsClientConn struct {
	conn *websocket.Conn
}

func (w *wsClientConn) WriteMessage(data []byte) error {
	return w.conn.WriteMessage(websocket.TextMessage, data)
}

func (w *wsClientConn) Close() error {
	return w.conn.Close()
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess := s.manager.Get(id)
	if sess == nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	if !sess.IsAlive() {
		http.Error(w, "session is dead", http.StatusGone)
		return
	}

	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("websocket upgrade failed: %v", err)
		return
	}

	clientConn := &wsClientConn{conn: conn}
	clientID := fmt.Sprintf("%s-%p", id, conn)
	sess.AddClient(clientID, clientConn)
	defer sess.RemoveClient(clientID)

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}

		var msg protocol.Message
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}

		switch msg.Type {
		case protocol.TypeInput:
			sess.WriteInput(msg.Data)
		case protocol.TypeResize:
			sess.Resize(msg.Cols, msg.Rows)
		}
	}
}

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
	return http.ListenAndServe(listenAddr, s.Handler())
}
