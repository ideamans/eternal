package session

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
)

type Manager struct {
	sessions map[string]*Session
	mu       sync.RWMutex
}

func NewManager() *Manager {
	return &Manager{
		sessions: make(map[string]*Session),
	}
}

func (m *Manager) Create(opts CreateOptions) (*Session, error) {
	if opts.ID == "" {
		opts.ID = generateID()
	}

	opts.OnExit = func(s *Session) {
		m.Remove(s.ID)
	}

	s, err := New(opts)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	m.sessions[s.ID] = s
	m.mu.Unlock()

	return s, nil
}

func (m *Manager) Get(id string) *Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessions[id]
}

// Find returns a session by ID or name.
func (m *Manager) Find(idOrName string) *Session {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Try exact ID match first
	if s, ok := m.sessions[idOrName]; ok {
		return s
	}

	// Try name match
	for _, s := range m.sessions {
		if s.Name == idOrName {
			return s
		}
	}

	// Try ID prefix match
	for id, s := range m.sessions {
		if len(idOrName) >= 4 && len(id) >= len(idOrName) && id[:len(idOrName)] == idOrName {
			return s
		}
	}

	return nil
}

func (m *Manager) List() []*Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	sessions := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		sessions = append(sessions, s)
	}
	// Sort by Dir ascending, then CreatedAt ascending
	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].Dir != sessions[j].Dir {
			return sessions[i].Dir < sessions[j].Dir
		}
		return sessions[i].CreatedAt.Before(sessions[j].CreatedAt)
	})
	return sessions
}

func (m *Manager) Remove(id string) {
	m.mu.Lock()
	s, ok := m.sessions[id]
	if ok {
		delete(m.sessions, id)
	}
	m.mu.Unlock()
	if ok {
		s.Close()
	}
}

func (m *Manager) KillAndRemove(id string) error {
	s := m.Get(id)
	if s == nil {
		return fmt.Errorf("session not found: %s", id)
	}
	if err := s.Kill(); err != nil {
		return err
	}
	// Remove will be called by onExit callback when process dies
	return nil
}

func generateID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}
