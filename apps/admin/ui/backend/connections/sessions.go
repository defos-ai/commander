package connections

import (
	"crypto/rand"
	"encoding/base64"
	"sort"
	"sync"
	"time"
)

const sessionLifetime = 8 * time.Hour

type credentials struct {
	username string
	password string
}

type Connection struct {
	URL      string `json:"url"`
	Username string `json:"username"`
}

type session struct {
	expires     time.Time
	connections map[string]*credentials
}

type sessions struct {
	mu      sync.Mutex
	items   map[string]*session
	now     func() time.Time
	stop    chan struct{}
	stopped chan struct{}
}

func newSessions(now func() time.Time) *sessions {
	s := &sessions{items: make(map[string]*session), now: now, stop: make(chan struct{}), stopped: make(chan struct{})}
	go s.cleanExpired()
	return s
}

func (s *sessions) close() {
	close(s.stop)
	<-s.stopped
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.items)
}

func (s *sessions) cleanExpired() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	defer close(s.stopped)
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			now := s.now()
			s.mu.Lock()
			for id, value := range s.items {
				if !value.expires.After(now) {
					delete(s.items, id)
				}
			}
			s.mu.Unlock()
		}
	}
}

func (s *sessions) activeLocked(id string) *session {
	value := s.items[id]
	if value != nil && !value.expires.After(s.now()) {
		delete(s.items, id)
		return nil
	}
	return value
}

func (s *sessions) save(id, target string, value *credentials) (string, time.Time, error) {
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", time.Time{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.activeLocked(id)
	delete(s.items, id)
	id = base64.RawURLEncoding.EncodeToString(random[:])
	if current == nil {
		current = &session{expires: s.now().Add(sessionLifetime), connections: make(map[string]*credentials)}
	}
	current.connections[target] = value
	s.items[id] = current
	return id, current.expires, nil
}

func (s *sessions) list(id string) ([]Connection, bool) {
	s.mu.Lock()
	items := make([]Connection, 0)
	current := s.activeLocked(id)
	if current == nil {
		s.mu.Unlock()
		return items, false
	}
	for target, value := range current.connections {
		items = append(items, Connection{URL: target, Username: value.username})
	}
	s.mu.Unlock()
	sort.Slice(items, func(i, j int) bool { return items[i].URL < items[j].URL })
	return items, true
}

func (s *sessions) get(id, target string) *credentials {
	s.mu.Lock()
	defer s.mu.Unlock()
	if current := s.activeLocked(id); current != nil {
		return current.connections[target]
	}
	return nil
}

func (s *sessions) remove(id, target string, observed *credentials) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.activeLocked(id)
	if current == nil {
		return true
	}
	if observed == nil || current.connections[target] == observed {
		delete(current.connections, target)
	}
	if len(current.connections) == 0 {
		delete(s.items, id)
		return true
	}
	return false
}
