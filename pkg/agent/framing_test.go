package agent

import (
	"net"
	"testing"

	"github.com/ideamans/eternal/pkg/protocol"
)

func TestWriteReadMsg(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	msg := protocol.Message{Type: protocol.TypeOutput, Data: []byte("hello world")}

	go func() {
		if err := WriteMsg(server, msg); err != nil {
			t.Errorf("WriteMsg: %v", err)
		}
	}()

	var got protocol.Message
	if err := ReadMsg(client, &got); err != nil {
		t.Fatalf("ReadMsg: %v", err)
	}

	if got.Type != protocol.TypeOutput {
		t.Errorf("type = %q, want %q", got.Type, protocol.TypeOutput)
	}
	if string(got.Data) != "hello world" {
		t.Errorf("data = %q, want %q", string(got.Data), "hello world")
	}
}

func TestWriteReadMsg_AgentHello(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	hello := protocol.AgentHello{
		Type:    protocol.TypeHello,
		ID:      "abc123",
		Command: []string{"bash"},
		Cols:    120,
		Rows:    40,
		Alive:   true,
	}

	go func() {
		WriteMsg(server, hello)
	}()

	var got protocol.AgentHello
	if err := ReadMsg(client, &got); err != nil {
		t.Fatalf("ReadMsg: %v", err)
	}

	if got.ID != "abc123" {
		t.Errorf("ID = %q, want %q", got.ID, "abc123")
	}
	if got.Cols != 120 || got.Rows != 40 {
		t.Errorf("size = %dx%d, want 120x40", got.Cols, got.Rows)
	}
	if !got.Alive {
		t.Error("alive = false, want true")
	}
}

func TestWriteReadMsg_MultipleMessages(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	messages := []protocol.Message{
		{Type: protocol.TypeOutput, Data: []byte("one")},
		{Type: protocol.TypeResize, Cols: 80, Rows: 24},
		{Type: protocol.TypeInput, Data: []byte("two")},
	}

	go func() {
		for _, m := range messages {
			WriteMsg(server, m)
		}
	}()

	for i, want := range messages {
		var got protocol.Message
		if err := ReadMsg(client, &got); err != nil {
			t.Fatalf("ReadMsg[%d]: %v", i, err)
		}
		if got.Type != want.Type {
			t.Errorf("[%d] type = %q, want %q", i, got.Type, want.Type)
		}
	}
}

func TestReadMsg_ConnectionClosed(t *testing.T) {
	server, client := net.Pipe()
	server.Close()

	var msg protocol.Message
	err := ReadMsg(client, &msg)
	if err == nil {
		t.Error("expected error on closed connection")
	}
	client.Close()
}
