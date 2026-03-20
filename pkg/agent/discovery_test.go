package agent

import (
	"os"
	"testing"
	"time"

	"github.com/ideamans/eternal/pkg/protocol"
)

func TestSocketDir_EnvOverride(t *testing.T) {
	t.Setenv("ETERNAL_SOCKET_DIR", "/tmp/test-eternal")
	if got := SocketDir(); got != "/tmp/test-eternal" {
		t.Errorf("SocketDir() = %q, want %q", got, "/tmp/test-eternal")
	}
}

func TestPaths(t *testing.T) {
	t.Setenv("ETERNAL_SOCKET_DIR", "/tmp/et")
	if got := SocketPath("abc"); got != "/tmp/et/abc.sock" {
		t.Errorf("SocketPath = %q", got)
	}
	if got := MetaPath("abc"); got != "/tmp/et/abc.json" {
		t.Errorf("MetaPath = %q", got)
	}
}

func TestWriteReadMeta(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ETERNAL_SOCKET_DIR", dir)

	meta := &protocol.AgentMeta{
		ID:        "test-id",
		Name:      "my-session",
		Command:   []string{"bash", "-l"},
		Dir:       "/home/user",
		Cols:      120,
		Rows:      40,
		CreatedAt: time.Now().Truncate(time.Second),
		PID:       os.Getpid(),
	}

	if err := WriteMeta(meta); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	got, err := ReadMeta("test-id")
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}

	if got.ID != "test-id" {
		t.Errorf("ID = %q", got.ID)
	}
	if got.Name != "my-session" {
		t.Errorf("Name = %q", got.Name)
	}
	if len(got.Command) != 2 || got.Command[0] != "bash" {
		t.Errorf("Command = %v", got.Command)
	}
	if got.Cols != 120 || got.Rows != 40 {
		t.Errorf("size = %dx%d", got.Cols, got.Rows)
	}
	if got.PID != os.Getpid() {
		t.Errorf("PID = %d, want %d", got.PID, os.Getpid())
	}
}

func TestListAgents_Empty(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ETERNAL_SOCKET_DIR", dir)

	agents, err := ListAgents()
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	if len(agents) != 0 {
		t.Errorf("len = %d, want 0", len(agents))
	}
}

func TestListAgents_LiveProcess(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ETERNAL_SOCKET_DIR", dir)

	// Write metadata with our own PID (definitely alive)
	meta := &protocol.AgentMeta{
		ID:        "live-session",
		Command:   []string{"cat"},
		Dir:       "/tmp",
		Cols:      80,
		Rows:      24,
		CreatedAt: time.Now(),
		PID:       os.Getpid(),
	}
	WriteMeta(meta)

	agents, err := ListAgents()
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("len = %d, want 1", len(agents))
	}
	if agents[0].ID != "live-session" {
		t.Errorf("ID = %q", agents[0].ID)
	}
}

func TestListAgents_StaleProcess(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ETERNAL_SOCKET_DIR", dir)

	// PID 99999999 should not exist
	meta := &protocol.AgentMeta{
		ID:        "dead-session",
		Command:   []string{"cat"},
		CreatedAt: time.Now(),
		PID:       99999999,
	}
	WriteMeta(meta)

	agents, err := ListAgents()
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	if len(agents) != 0 {
		t.Errorf("len = %d, want 0 (stale should be cleaned)", len(agents))
	}

	// Metadata file should be cleaned up
	if _, err := os.Stat(MetaPath("dead-session")); !os.IsNotExist(err) {
		t.Error("stale metadata file should be removed")
	}
}

func TestListAgents_Sorting(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ETERNAL_SOCKET_DIR", dir)

	pid := os.Getpid()
	now := time.Now()

	WriteMeta(&protocol.AgentMeta{ID: "b", Dir: "/z", CreatedAt: now, PID: pid})
	WriteMeta(&protocol.AgentMeta{ID: "a", Dir: "/a", CreatedAt: now.Add(time.Second), PID: pid})
	WriteMeta(&protocol.AgentMeta{ID: "c", Dir: "/a", CreatedAt: now, PID: pid})

	agents, _ := ListAgents()
	if len(agents) != 3 {
		t.Fatalf("len = %d, want 3", len(agents))
	}
	// /a before /z, within /a: earlier CreatedAt first
	if agents[0].ID != "c" || agents[1].ID != "a" || agents[2].ID != "b" {
		t.Errorf("order = [%s, %s, %s], want [c, a, b]", agents[0].ID, agents[1].ID, agents[2].ID)
	}
}

func TestFindAgent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ETERNAL_SOCKET_DIR", dir)

	pid := os.Getpid()
	WriteMeta(&protocol.AgentMeta{ID: "abcd1234", Name: "web", CreatedAt: time.Now(), PID: pid})
	WriteMeta(&protocol.AgentMeta{ID: "efgh5678", Name: "api", CreatedAt: time.Now(), PID: pid})

	// By exact ID
	m, err := FindAgent("abcd1234")
	if err != nil || m.ID != "abcd1234" {
		t.Errorf("find by ID: %v, %v", m, err)
	}

	// By name
	m, err = FindAgent("api")
	if err != nil || m.ID != "efgh5678" {
		t.Errorf("find by name: %v, %v", m, err)
	}

	// By ID prefix
	m, err = FindAgent("abcd")
	if err != nil || m.ID != "abcd1234" {
		t.Errorf("find by prefix: %v, %v", m, err)
	}

	// Not found
	_, err = FindAgent("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent")
	}
}

func TestCleanStale(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ETERNAL_SOCKET_DIR", dir)

	// Create stale entry
	WriteMeta(&protocol.AgentMeta{ID: "stale", PID: 99999999, CreatedAt: time.Now()})
	// Create socket file too
	os.WriteFile(SocketPath("stale"), []byte{}, 0600)

	// Create live entry
	WriteMeta(&protocol.AgentMeta{ID: "live", PID: os.Getpid(), CreatedAt: time.Now()})

	CleanStale()

	// Stale should be gone
	if _, err := os.Stat(MetaPath("stale")); !os.IsNotExist(err) {
		t.Error("stale meta should be removed")
	}
	if _, err := os.Stat(SocketPath("stale")); !os.IsNotExist(err) {
		t.Error("stale socket should be removed")
	}

	// Live should remain
	if _, err := os.Stat(MetaPath("live")); err != nil {
		t.Error("live meta should remain")
	}
}
