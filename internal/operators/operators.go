// Package operators manages dashboard operator accounts.
//
// This is the center's own governance surface. It never configures Scoot node
// policy and never stores cleartext passwords. Passwords are hashed with
// PBKDF2-HMAC-SHA256 using only the Go standard library to preserve the
// single-binary, stdlib-first posture.
package operators

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	passwordAlgo = "pbkdf2-sha256"
	iterations   = 120_000
	saltBytes    = 16
	keyBytes     = 32
)

var (
	ErrNotFound       = errors.New("operator not found")
	ErrDuplicate      = errors.New("operator already exists")
	ErrInvalid        = errors.New("invalid operator")
	ErrBadCredentials = errors.New("bad credentials")
)

// Operator is the persisted dashboard operator account. PasswordHash is never
// returned in public snapshots.
type Operator struct {
	Username     string `json:"username"`
	DisplayName  string `json:"display_name,omitempty"`
	Email        string `json:"email,omitempty"`
	PasswordHash string `json:"password_hash"`
	CreatedMS    int64  `json:"created_ms"`
	UpdatedMS    int64  `json:"updated_ms"`
	LastLoginMS  int64  `json:"last_login_ms,omitempty"`
}

// Snapshot is a dashboard/API-safe operator view.
type Snapshot struct {
	Username    string `json:"username"`
	DisplayName string `json:"display_name,omitempty"`
	Email       string `json:"email,omitempty"`
	CreatedMS   int64  `json:"created_ms"`
	UpdatedMS   int64  `json:"updated_ms"`
	LastLoginMS int64  `json:"last_login_ms,omitempty"`
}

// Store persists operators to dataDir/operators.json. Empty dataDir creates an
// in-memory store for tests.
type Store struct {
	mu    sync.Mutex
	path  string
	users map[string]Operator
}

// Open loads the operator store and bootstraps the first operator when the store
// is empty and bootstrapPassword is provided.
func Open(dataDir, bootstrapUser, bootstrapPassword string, now time.Time) (*Store, error) {
	s := &Store{users: map[string]Operator{}}
	if dataDir != "" {
		if err := os.MkdirAll(dataDir, 0o700); err != nil {
			return nil, fmt.Errorf("create data dir: %w", err)
		}
		s.path = filepath.Join(dataDir, "operators.json")
		if err := s.load(); err != nil {
			return nil, err
		}
	}
	if len(s.users) == 0 && bootstrapPassword != "" {
		if err := s.createLocked(bootstrapUser, bootstrapUser, "", bootstrapPassword, now); err != nil {
			return nil, err
		}
		if err := s.saveLocked(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *Store) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read operators file: %w", err)
	}
	var users []Operator
	if err := json.Unmarshal(data, &users); err != nil {
		return fmt.Errorf("parse operators file: %w", err)
	}
	for _, u := range users {
		if u.Username == "" || u.PasswordHash == "" {
			continue
		}
		s.users[u.Username] = u
	}
	return nil
}

func (s *Store) saveLocked() error {
	if s.path == "" {
		return nil
	}
	users := make([]Operator, 0, len(s.users))
	for _, u := range s.users {
		users = append(users, u)
	}
	sort.Slice(users, func(i, j int) bool { return users[i].Username < users[j].Username })
	data, err := json.MarshalIndent(users, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write operators file: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("replace operators file: %w", err)
	}
	return nil
}

func (s *Store) Configured() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.users) > 0
}

func (s *Store) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.users)
}

func (s *Store) Authenticate(username, password string, now time.Time) (Snapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[normalizeUsername(username)]
	if !ok || !verifyPassword(u.PasswordHash, password) {
		return Snapshot{}, false
	}
	u.LastLoginMS = now.UnixMilli()
	u.UpdatedMS = now.UnixMilli()
	s.users[u.Username] = u
	_ = s.saveLocked()
	return snapshot(u), true
}

func (s *Store) List() []Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Snapshot, 0, len(s.users))
	for _, u := range s.users {
		out = append(out, snapshot(u))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Username < out[j].Username })
	return out
}

func (s *Store) Get(username string) (Snapshot, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[normalizeUsername(username)]
	if !ok {
		return Snapshot{}, false
	}
	return snapshot(u), true
}

func (s *Store) Create(username, displayName, email, password string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.createLocked(username, displayName, email, password, now); err != nil {
		return err
	}
	return s.saveLocked()
}

func (s *Store) createLocked(username, displayName, email, password string, now time.Time) error {
	username = normalizeUsername(username)
	if username == "" || password == "" {
		return ErrInvalid
	}
	if _, ok := s.users[username]; ok {
		return ErrDuplicate
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	ms := now.UnixMilli()
	s.users[username] = Operator{
		Username:     username,
		DisplayName:  strings.TrimSpace(displayName),
		Email:        strings.TrimSpace(email),
		PasswordHash: hash,
		CreatedMS:    ms,
		UpdatedMS:    ms,
	}
	return nil
}

func (s *Store) UpdateProfile(username, displayName, email string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	username = normalizeUsername(username)
	u, ok := s.users[username]
	if !ok {
		return ErrNotFound
	}
	u.DisplayName = strings.TrimSpace(displayName)
	u.Email = strings.TrimSpace(email)
	u.UpdatedMS = now.UnixMilli()
	s.users[username] = u
	return s.saveLocked()
}

func (s *Store) SetPassword(username, newPassword string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	username = normalizeUsername(username)
	u, ok := s.users[username]
	if !ok {
		return ErrNotFound
	}
	if newPassword == "" {
		return ErrInvalid
	}
	hash, err := hashPassword(newPassword)
	if err != nil {
		return err
	}
	u.PasswordHash = hash
	u.UpdatedMS = now.UnixMilli()
	s.users[username] = u
	return s.saveLocked()
}

func (s *Store) ChangePassword(username, currentPassword, newPassword string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	username = normalizeUsername(username)
	u, ok := s.users[username]
	if !ok {
		return ErrNotFound
	}
	if !verifyPassword(u.PasswordHash, currentPassword) {
		return ErrBadCredentials
	}
	if newPassword == "" {
		return ErrInvalid
	}
	hash, err := hashPassword(newPassword)
	if err != nil {
		return err
	}
	u.PasswordHash = hash
	u.UpdatedMS = now.UnixMilli()
	s.users[username] = u
	return s.saveLocked()
}

func snapshot(u Operator) Snapshot {
	return Snapshot{
		Username:    u.Username,
		DisplayName: u.DisplayName,
		Email:       u.Email,
		CreatedMS:   u.CreatedMS,
		UpdatedMS:   u.UpdatedMS,
		LastLoginMS: u.LastLoginMS,
	}
}

func normalizeUsername(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}

func hashPassword(password string) (string, error) {
	salt := make([]byte, saltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := pbkdf2Key([]byte(password), salt, iterations, keyBytes, sha256.New)
	return strings.Join([]string{
		passwordAlgo,
		strconv.Itoa(iterations),
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	}, "$"), nil
}

func verifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != passwordAlgo {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter <= 0 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return false
	}
	got := pbkdf2Key([]byte(password), salt, iter, len(want), sha256.New)
	return subtle.ConstantTimeCompare(got, want) == 1
}

func pbkdf2Key(password, salt []byte, iter, keyLen int, h func() hash.Hash) []byte {
	prf := hmac.New(h, password)
	hLen := prf.Size()
	numBlocks := (keyLen + hLen - 1) / hLen
	var out []byte
	var buf [4]byte
	for block := 1; block <= numBlocks; block++ {
		prf.Reset()
		prf.Write(salt)
		buf[0] = byte(block >> 24)
		buf[1] = byte(block >> 16)
		buf[2] = byte(block >> 8)
		buf[3] = byte(block)
		prf.Write(buf[:])
		u := prf.Sum(nil)
		t := append([]byte(nil), u...)
		for i := 1; i < iter; i++ {
			prf.Reset()
			prf.Write(u)
			u = prf.Sum(nil)
			for x := range t {
				t[x] ^= u[x]
			}
		}
		out = append(out, t...)
	}
	return out[:keyLen]
}
