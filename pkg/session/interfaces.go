package session

import "os"

// PTY abstracts a pseudo-terminal attached to a running process.
type PTY interface {
	Read(p []byte) (n int, err error)
	Write(p []byte) (n int, err error)
	Resize(cols, rows int) error
	Close() error
}

// Process abstracts a running OS process.
type Process interface {
	Wait() (exitCode int, err error)
	Signal(sig os.Signal) error
}

// PTYFactory creates a PTY+Process pair from a command specification.
type PTYFactory interface {
	Start(command []string, dir string, env []string, cols, rows int) (PTY, Process, error)
}

// ClientConn abstracts a single connection that can receive messages.
type ClientConn interface {
	WriteMessage(data []byte) error
	Close() error
}

// OSEnv provides OS-level information needed for session creation.
type OSEnv interface {
	Environ() []string
	Stat(path string) (os.FileInfo, error)
}
