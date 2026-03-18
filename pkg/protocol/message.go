package protocol

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
)
