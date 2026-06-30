package center

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// sessionCookie is the dashboard login cookie name.
const sessionCookie = "scootship_session"

type session struct {
	user    string
	csrf    string
	expires time.Time
}

// sessionStore is a tiny in-memory dashboard session manager. Keeping it
// stdlib-only and in-memory matches the single-binary posture; sessions are not
// durable across restarts, which is an acceptable tradeoff for an admin console
// (operators simply log in again). Node auth is unaffected — edges use bearer
// tokens, never sessions.
type sessionStore struct {
	mu       sync.Mutex
	sessions map[string]session
	ttl      time.Duration
}

func newSessionStore(ttl time.Duration) *sessionStore {
	return &sessionStore{sessions: make(map[string]session), ttl: ttl}
}

// create mints a cryptographically random session token for user.
func (s *sessionStore) create(user string, ttl time.Duration) (string, time.Time, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", time.Time{}, err
	}
	csrfBuf := make([]byte, 32)
	if _, err := rand.Read(csrfBuf); err != nil {
		return "", time.Time{}, err
	}
	token := hex.EncodeToString(buf)
	csrf := hex.EncodeToString(csrfBuf)
	if ttl <= 0 {
		ttl = s.ttl
	}
	exp := time.Now().Add(ttl)
	s.mu.Lock()
	s.sessions[token] = session{user: user, csrf: csrf, expires: exp}
	s.mu.Unlock()
	return token, exp, nil
}

// validate returns the session user if the token is known and unexpired.
func (s *sessionStore) validate(token string) (string, bool) {
	if token == "" {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[token]
	if !ok {
		return "", false
	}
	if time.Now().After(sess.expires) {
		delete(s.sessions, token)
		return "", false
	}
	return sess.user, true
}

func (s *sessionStore) csrf(token string) (string, bool) {
	if token == "" {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[token]
	if !ok {
		return "", false
	}
	if time.Now().After(sess.expires) {
		delete(s.sessions, token)
		return "", false
	}
	return sess.csrf, true
}

// destroy removes a session (logout).
func (s *sessionStore) destroy(token string) {
	if token == "" {
		return
	}
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()
}
