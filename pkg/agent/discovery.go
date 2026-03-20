package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"

	"github.com/ideamans/eternal/pkg/protocol"
)

// SocketDir returns the directory where agent sockets and metadata are stored.
func SocketDir() string {
	if dir := os.Getenv("ETERNAL_SOCKET_DIR"); dir != "" {
		return dir
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(os.TempDir(), fmt.Sprintf("eternal-%d", os.Getuid()))
	}
	// Linux: prefer XDG_RUNTIME_DIR
	if xdg := os.Getenv("XDG_RUNTIME_DIR"); xdg != "" {
		return filepath.Join(xdg, "eternal")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("eternal-%d", os.Getuid()))
}

// EnsureSocketDir creates the socket directory with 0700 permissions.
func EnsureSocketDir() error {
	return os.MkdirAll(SocketDir(), 0700)
}

// SocketPath returns the Unix socket path for a given session ID.
func SocketPath(id string) string {
	return filepath.Join(SocketDir(), id+".sock")
}

// MetaPath returns the metadata file path for a given session ID.
func MetaPath(id string) string {
	return filepath.Join(SocketDir(), id+".json")
}

// WriteMeta writes the agent metadata to disk.
func WriteMeta(meta *protocol.AgentMeta) error {
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return os.WriteFile(MetaPath(meta.ID), data, 0600)
}

// ReadMeta reads the agent metadata from disk.
func ReadMeta(id string) (*protocol.AgentMeta, error) {
	data, err := os.ReadFile(MetaPath(id))
	if err != nil {
		return nil, err
	}
	var meta protocol.AgentMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

// ListAgents scans the socket directory and returns metadata for all live agents.
func ListAgents() ([]*protocol.AgentMeta, error) {
	dir := SocketDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var agents []*protocol.AgentMeta
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		meta, err := ReadMeta(id)
		if err != nil {
			continue
		}
		// Check if agent process is still alive
		if !isProcessAlive(meta.PID) {
			// Stale: clean up
			os.Remove(MetaPath(id))
			os.Remove(SocketPath(id))
			continue
		}
		agents = append(agents, meta)
	}

	sort.Slice(agents, func(i, j int) bool {
		if agents[i].Dir != agents[j].Dir {
			return agents[i].Dir < agents[j].Dir
		}
		return agents[i].CreatedAt.Before(agents[j].CreatedAt)
	})

	return agents, nil
}

// FindAgent finds an agent by exact ID, name, or ID prefix.
func FindAgent(idOrName string) (*protocol.AgentMeta, error) {
	agents, err := ListAgents()
	if err != nil {
		return nil, err
	}
	// Exact ID
	for _, a := range agents {
		if a.ID == idOrName {
			return a, nil
		}
	}
	// Name match
	for _, a := range agents {
		if a.Name == idOrName {
			return a, nil
		}
	}
	// ID prefix
	for _, a := range agents {
		if len(idOrName) >= 4 && strings.HasPrefix(a.ID, idOrName) {
			return a, nil
		}
	}
	return nil, fmt.Errorf("session not found: %s", idOrName)
}

// CleanStale removes socket and metadata files for dead agent processes.
func CleanStale() {
	dir := SocketDir()
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		meta, err := ReadMeta(id)
		if err != nil || !isProcessAlive(meta.PID) {
			os.Remove(MetaPath(id))
			os.Remove(SocketPath(id))
		}
	}
}

func isProcessAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Unix, signal 0 checks if process exists
	err = proc.Signal(syscall.Signal(0))
	return err == nil
}
