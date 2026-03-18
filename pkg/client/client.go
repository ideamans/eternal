package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/gorilla/websocket"
	"github.com/ideamans/eternal/pkg/protocol"
	"github.com/ideamans/eternal/pkg/session"
)

type Client struct {
	BaseURL string // e.g. "http://127.0.0.1:2840"
}

func New(baseURL string) *Client {
	return &Client{BaseURL: baseURL}
}

type CreateRequest struct {
	Name    string   `json:"name"`
	Command []string `json:"command"`
	Dir     string   `json:"dir"`
	Cols    int      `json:"cols"`
	Rows    int      `json:"rows"`
}

func (c *Client) CreateSession(req CreateRequest) (*session.Session, error) {
	body, _ := json.Marshal(req)
	resp, err := http.Post(c.BaseURL+"/api/sessions", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to server: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		data, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("server error (%d): %s", resp.StatusCode, data)
	}

	var sess session.Session
	if err := json.NewDecoder(resp.Body).Decode(&sess); err != nil {
		return nil, err
	}
	return &sess, nil
}

func (c *Client) ListSessions() ([]*session.Session, error) {
	resp, err := http.Get(c.BaseURL + "/api/sessions")
	if err != nil {
		return nil, fmt.Errorf("failed to connect to server: %w", err)
	}
	defer resp.Body.Close()

	var sessions []*session.Session
	if err := json.NewDecoder(resp.Body).Decode(&sessions); err != nil {
		return nil, err
	}
	return sessions, nil
}

func (c *Client) KillSession(id string) error {
	req, _ := http.NewRequest(http.MethodDelete, c.BaseURL+"/api/sessions/"+id, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to connect to server: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server error (%d): %s", resp.StatusCode, data)
	}
	return nil
}

// ConnectWebSocket establishes a WebSocket connection to a session.
// Returns the connection and a channel that receives the exit code when the session ends.
func (c *Client) ConnectWebSocket(sessionID string) (*websocket.Conn, error) {
	wsURL := "ws" + c.BaseURL[4:] + "/ws/session/" + sessionID
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to connect websocket: %w", err)
	}
	return conn, nil
}

func SendInput(conn *websocket.Conn, data []byte) error {
	msg := protocol.Message{Type: protocol.TypeInput, Data: data}
	raw, _ := json.Marshal(msg)
	return conn.WriteMessage(websocket.TextMessage, raw)
}

func SendResize(conn *websocket.Conn, cols, rows int) error {
	msg := protocol.Message{Type: protocol.TypeResize, Cols: cols, Rows: rows}
	raw, _ := json.Marshal(msg)
	return conn.WriteMessage(websocket.TextMessage, raw)
}
