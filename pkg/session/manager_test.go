package session

import (
	"testing"
	"time"
)

func createTestSession(m *Manager, id, name, dir string, fp *fakePTY, proc *fakeProcess) *Session {
	s, _ := m.Create(CreateOptions{
		ID:         id,
		Name:       name,
		Command:    []string{"fake"},
		Dir:        dir,
		Cols:       80,
		Rows:       24,
		PTYFactory: &fakePTYFactory{pty: fp, process: proc},
		OSEnv:      &fakeOSEnv{env: []string{}, isDir: true},
	})
	return s
}

func TestManagerCreateAndGet(t *testing.T) {
	m := NewManager()
	fp := newFakePTY()
	proc := newFakeProcess(0)
	defer func() { fp.Close(); close(proc.done) }()

	s := createTestSession(m, "s1", "mysession", "/tmp", fp, proc)
	if s == nil {
		t.Fatal("Create returned nil")
	}

	got := m.Get("s1")
	if got != s {
		t.Error("Get did not return the created session")
	}

	got = m.Get("nonexistent")
	if got != nil {
		t.Error("Get should return nil for unknown ID")
	}
}

func TestManagerFindByName(t *testing.T) {
	m := NewManager()
	fp := newFakePTY()
	proc := newFakeProcess(0)
	defer func() { fp.Close(); close(proc.done) }()

	createTestSession(m, "abc12345", "myname", "/tmp", fp, proc)

	// Find by name
	got := m.Find("myname")
	if got == nil || got.Name != "myname" {
		t.Error("Find by name failed")
	}

	// Find by ID prefix
	got = m.Find("abc1")
	if got == nil || got.ID != "abc12345" {
		t.Error("Find by ID prefix failed")
	}

	// Find by exact ID
	got = m.Find("abc12345")
	if got == nil {
		t.Error("Find by exact ID failed")
	}

	// Not found
	got = m.Find("zzz")
	if got != nil {
		t.Error("Find should return nil for unknown")
	}
}

func TestManagerListSorting(t *testing.T) {
	m := NewManager()

	// Create sessions with different dirs - use separate fakes for each
	fp1 := newFakePTY()
	proc1 := newFakeProcess(0)
	defer func() { fp1.Close(); close(proc1.done) }()

	fp2 := newFakePTY()
	proc2 := newFakeProcess(0)
	defer func() { fp2.Close(); close(proc2.done) }()

	fp3 := newFakePTY()
	proc3 := newFakeProcess(0)
	defer func() { fp3.Close(); close(proc3.done) }()

	createTestSession(m, "s1", "a", "/home/user/zzz", fp1, proc1)
	time.Sleep(time.Millisecond)
	createTestSession(m, "s2", "b", "/home/user/aaa", fp2, proc2)
	time.Sleep(time.Millisecond)
	createTestSession(m, "s3", "c", "/home/user/aaa", fp3, proc3)

	list := m.List()
	if len(list) != 3 {
		t.Fatalf("list len = %d, want 3", len(list))
	}

	// Should be sorted: aaa(s2), aaa(s3), zzz(s1)
	if list[0].ID != "s2" {
		t.Errorf("list[0].ID = %q, want s2", list[0].ID)
	}
	if list[1].ID != "s3" {
		t.Errorf("list[1].ID = %q, want s3", list[1].ID)
	}
	if list[2].ID != "s1" {
		t.Errorf("list[2].ID = %q, want s1", list[2].ID)
	}
}

func TestManagerKillAndRemove(t *testing.T) {
	m := NewManager()
	fp := newFakePTY()
	proc := newFakeProcess(0)

	createTestSession(m, "s1", "test", "/tmp", fp, proc)

	err := m.KillAndRemove("s1")
	if err != nil {
		t.Fatalf("KillAndRemove error: %v", err)
	}

	// Signal should have been sent
	signals := proc.getSignals()
	if len(signals) == 0 {
		t.Error("no signal sent")
	}

	// Process exits, onExit fires, session removed
	close(proc.done)

	waitUntil(t, time.Second, func() bool {
		return m.Get("s1") == nil
	})
}

func TestManagerKillNonexistent(t *testing.T) {
	m := NewManager()
	err := m.KillAndRemove("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent session")
	}
}

func TestManagerAutoRemoveOnExit(t *testing.T) {
	m := NewManager()
	fp := newFakePTY()
	proc := newFakeProcess(0)

	createTestSession(m, "s1", "test", "/tmp", fp, proc)

	if len(m.List()) != 1 {
		t.Fatal("session not created")
	}

	// Simulate process exit
	fp.Close()
	close(proc.done)

	waitUntil(t, time.Second, func() bool {
		return len(m.List()) == 0
	})
}

func TestManagerFindShortPrefix(t *testing.T) {
	m := NewManager()
	fp := newFakePTY()
	proc := newFakeProcess(0)
	defer func() { fp.Close(); close(proc.done) }()

	createTestSession(m, "abcdef12", "test", "/tmp", fp, proc)

	// Prefix "abc" is only 3 chars, should NOT match via prefix
	got := m.Find("abc")
	if got != nil {
		t.Error("Find should not match prefix shorter than 4 characters")
	}

	// Prefix "abcd" is 4 chars, should match
	got = m.Find("abcd")
	if got == nil || got.ID != "abcdef12" {
		t.Error("Find should match prefix of 4+ characters")
	}
}
