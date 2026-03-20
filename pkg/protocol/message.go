package protocol

import "time"

// WebSocket message types exchanged between server and clients.
type Message struct {
	Type     string `json:"type"`
	Data     []byte `json:"data,omitempty"`
	Cols     int    `json:"cols,omitempty"`
	Rows     int    `json:"rows,omitempty"`
	ExitCode *int   `json:"exit_code,omitempty"`
}

// Message type constants.
const (
	TypeInput  = "input"
	TypeOutput = "output"
	TypeResize = "resize"
	TypeExit   = "exit"
	TypeHello  = "hello"
	TypeKill   = "kill"
)

// AgentHello is sent by the agent to each new Unix socket connection.
type AgentHello struct {
	Type       string    `json:"type"`
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Command    []string  `json:"command"`
	Dir        string    `json:"dir"`
	Cols       int       `json:"cols"`
	Rows       int       `json:"rows"`
	CreatedAt  time.Time `json:"created_at"`
	Scrollback []byte    `json:"scrollback,omitempty"`
	Alive      bool      `json:"alive"`
	ExitCode   *int      `json:"exit_code,omitempty"`
}

// AgentMeta is the metadata file written by the agent for quick discovery.
type AgentMeta struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Command   []string  `json:"command"`
	Dir       string    `json:"dir"`
	Cols      int       `json:"cols"`
	Rows      int       `json:"rows"`
	CreatedAt time.Time `json:"created_at"`
	PID       int       `json:"pid"`
}
