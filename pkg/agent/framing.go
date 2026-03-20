package agent

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
)

// WriteMsg writes a length-prefixed JSON message to a connection.
func WriteMsg(conn net.Conn, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, uint32(len(data)))
	if _, err := conn.Write(header); err != nil {
		return err
	}
	_, err = conn.Write(data)
	return err
}

// ReadMsg reads a length-prefixed JSON message from a connection.
func ReadMsg(conn net.Conn, v any) error {
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return err
	}
	length := binary.BigEndian.Uint32(header)
	if length > 4*1024*1024 { // 4MB max
		return fmt.Errorf("message too large: %d bytes", length)
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(conn, data); err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
