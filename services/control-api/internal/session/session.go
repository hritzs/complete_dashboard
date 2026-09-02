package session

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// Session holds the data for a single user session.
type Session struct {
	UpstreamToken string    `json:"-"`
	Username      string    `json:"username"`
	BrokerID      string    `json:"brokerId"`
	CreatedAt     time.Time `json:"-"`
	GCID          string    `json:"gcid,omitempty"`
	WSSessionID   string    `json:"websocketSessionId,omitempty"`
	IrisIP        string    `json:"interactiveSocketIp,omitempty"`
	IrisPort      string    `json:"interactiveSocketPort,omitempty"`
	ApolloIP      string    `json:"marketdataSocketIp,omitempty"`
	ApolloPort    string    `json:"marketdataSocketPort,omitempty"`
}

// Store is a simple in-memory session store.
type Store struct {
	sessions map[string]Session
	mu       sync.RWMutex
}

func NewStore() *Store {
	return &Store{
		sessions: make(map[string]Session),
	}
}

func (s *Store) Create(session Session) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	sessionID := uuid.NewString()
	s.sessions[sessionID] = session
	return sessionID
}

func (s *Store) Get(sessionID string) (Session, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	session, ok := s.sessions[sessionID]
	return session, ok
}

func (s *Store) Update(sessionID string, session Session) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.sessions[sessionID]; !ok {
		return false
	}
	s.sessions[sessionID] = session
	return true
}

func (s *Store) Delete(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.sessions, sessionID)
}