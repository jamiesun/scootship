// Package tokens authenticates edge nodes by per-node bearer token.
//
// Per-node (not fleet-shared) tokens let the center identify, rate-limit, and
// revoke a single node without rotating the whole fleet (EDGE.md). Tokens are
// loaded from a private file or the environment and are never logged. This is
// the center's own governance surface; it is not, and must not become,
// configuration of any node's local execution policy.
package tokens

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// Source names where a token was configured. These are safe to show in the
// dashboard because they disclose configuration provenance, not token material.
const (
	SourceFile    = "file"
	SourceInline  = "inline"
	SourceDev     = "dev"
	SourceMemory  = "memory"
	SourceManaged = "managed"
)

var (
	ErrInvalidEntry   = errors.New("invalid token entry")
	ErrDuplicateToken = errors.New("duplicate token")
)

// Entry is one node token before it is loaded into a Registry.
type Entry struct {
	NodeID string
	Token  string
	Source string
}

// Snapshot is the dashboard/API-safe view of one configured node token. It never
// contains the bearer token itself.
type Snapshot struct {
	NodeID              string `json:"node_id"`
	Source              string `json:"source"`
	Fingerprint         string `json:"fingerprint"`
	LastAuthenticatedMS int64  `json:"last_authenticated_ms,omitempty"`
}

type record struct {
	nodeID      string
	source      string
	fingerprint string
	lastAuth    time.Time
}

// Registry maps presented bearer tokens to node_ids and keeps only safe metadata
// for the dashboard. It intentionally never exposes token material.
type Registry struct {
	mu           sync.Mutex
	byToken      map[string]*record // token -> metadata; token is private to this package
	managed      *ManagedStore
	managedState ManagedState
}

// New builds a registry from a node_id -> token map. It is kept for tests and
// simple callers; entries are marked as memory-sourced.
func New(nodeToToken map[string]string) *Registry {
	entries := make([]Entry, 0, len(nodeToToken))
	for node, tok := range nodeToToken {
		entries = append(entries, Entry{NodeID: node, Token: tok, Source: SourceMemory})
	}
	return NewEntries(entries)
}

// NewEntries builds a registry from explicit entries with source metadata.
func NewEntries(entries []Entry) *Registry {
	return newEntries(entries, nil, ManagedState{})
}

// NewEntriesWithManaged builds a registry from static entries plus a center-
// managed token lifecycle state. Managed tokens override static entries for the
// same node, and managed revocations suppress static entries on load.
func NewEntriesWithManaged(entries []Entry, managed *ManagedStore) (*Registry, error) {
	state := ManagedState{}
	if managed != nil {
		var err error
		state, err = managed.Load()
		if err != nil {
			return nil, err
		}
		entries = ApplyManagedState(entries, state)
	}
	return newEntries(entries, managed, state), nil
}

func newEntries(entries []Entry, managed *ManagedStore, state ManagedState) *Registry {
	r := &Registry{
		byToken:      make(map[string]*record, len(entries)),
		managed:      managed,
		managedState: normalizeManagedState(state),
	}
	byNode := map[string]string{}
	for _, e := range entries {
		node, tok, source, err := normalizeEntry(e.NodeID, e.Token, e.Source)
		if err != nil {
			continue
		}
		if prevToken, ok := byNode[node]; ok {
			delete(r.byToken, prevToken)
		}
		r.byToken[tok] = &record{nodeID: node, source: source, fingerprint: fingerprint(tok)}
		byNode[node] = tok
	}
	return r
}

// NodeFor returns the node a token authenticates. It scans configured secrets
// with constant-time comparisons instead of using token material as a direct
// lookup key on the request path.
func (r *Registry) NodeFor(token string) (string, bool) {
	return r.NodeForAt(token, time.Now())
}

// NodeForAt is NodeFor with an injected clock for tests.
func (r *Registry) NodeForAt(token string, now time.Time) (string, bool) {
	if token == "" {
		return "", false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var rec *record
	matched := false
	for known, knownRec := range r.byToken {
		if subtle.ConstantTimeCompare([]byte(token), []byte(known)) == 1 {
			rec = knownRec
			matched = true
		}
	}
	if !matched {
		return "", false
	}
	rec.lastAuth = now
	return rec.nodeID, true
}

// Empty reports whether no tokens are configured (no node can authenticate).
func (r *Registry) Empty() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.byToken) == 0
}

// Count returns the number of configured node tokens.
func (r *Registry) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.byToken)
}

// Snapshots returns dashboard/API-safe token inventory, sorted by node_id.
func (r *Registry) Snapshots() []Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Snapshot, 0, len(r.byToken))
	for _, rec := range r.byToken {
		s := Snapshot{NodeID: rec.nodeID, Source: rec.source, Fingerprint: rec.fingerprint}
		if !rec.lastAuth.IsZero() {
			s.LastAuthenticatedMS = rec.lastAuth.UnixMilli()
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].NodeID != out[j].NodeID {
			return out[i].NodeID < out[j].NodeID
		}
		return out[i].Fingerprint < out[j].Fingerprint
	})
	return out
}

// UpsertManaged creates or rotates a center-managed token for nodeID. The token
// secret is stored only in the private managed token file when one is
// configured; callers must never echo it in responses or logs.
func (r *Registry) UpsertManaged(nodeID, token string) error {
	node, tok, source, err := normalizeEntry(nodeID, token, SourceManaged)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if rec, ok := r.byToken[tok]; ok && rec.nodeID != node {
		return ErrDuplicateToken
	}

	state := normalizeManagedState(r.managedState)
	if state.Tokens == nil {
		state.Tokens = map[string]string{}
	}
	state.Tokens[node] = tok
	state.Revoked = removeString(state.Revoked, node)
	state = normalizeManagedState(state)
	if r.managed != nil {
		if err := r.managed.Save(state); err != nil {
			return err
		}
	}
	r.managedState = state
	r.setLocked(node, tok, source)
	return nil
}

// RevokeManaged revokes a node token at the center. For statically configured
// tokens, the revocation is persisted in managed state so the static secret is
// suppressed on restart without editing the operator-owned source file/env.
func (r *Registry) RevokeManaged(nodeID string) error {
	node := strings.TrimSpace(nodeID)
	if node == "" {
		return ErrInvalidEntry
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	state := normalizeManagedState(r.managedState)
	delete(state.Tokens, node)
	if !containsString(state.Revoked, node) {
		state.Revoked = append(state.Revoked, node)
	}
	state = normalizeManagedState(state)
	if r.managed != nil {
		if err := r.managed.Save(state); err != nil {
			return err
		}
	}
	r.managedState = state
	r.removeNodeLocked(node)
	return nil
}

func (r *Registry) setLocked(node, tok, source string) {
	r.removeNodeLocked(node)
	r.byToken[tok] = &record{nodeID: node, source: source, fingerprint: fingerprint(tok)}
}

func (r *Registry) removeNodeLocked(node string) {
	for tok, rec := range r.byToken {
		if rec.nodeID == node {
			delete(r.byToken, tok)
		}
	}
}

// LoadFile reads a JSON object mapping node_id -> token from path. The file must
// be present, regular, and private: no executable, group, or world permissions
// are allowed. Mode 0600 is the normal setting; stricter owner-only modes are
// also accepted.
func LoadFile(path string) (map[string]string, error) {
	entries, err := LoadFileEntries(path)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(entries))
	for _, e := range entries {
		out[e.NodeID] = e.Token
	}
	return out, nil
}

// LoadFileEntries is LoadFile plus source metadata for token inventory.
func LoadFileEntries(path string) ([]Entry, error) {
	if err := checkPrivateFile(path); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read tokens file: %w", err)
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse tokens file (want JSON object of node_id->token): %w", err)
	}
	return entriesFromMap(m, SourceFile), nil
}

func checkPrivateFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat tokens file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("tokens file must be a regular file: %s", path)
	}
	if p := info.Mode().Perm(); p&0o177 != 0 {
		return fmt.Errorf("tokens file %s has insecure permissions %04o (want 0600 or stricter)", path, p)
	}
	return nil
}

// ParseInline parses "node_a=tokenA,node_b=tokenB" into a node_id -> token map.
func ParseInline(s string) map[string]string {
	entries := ParseInlineEntries(s)
	out := map[string]string{}
	for _, e := range entries {
		out[e.NodeID] = e.Token
	}
	return out
}

// ParseInlineEntries is ParseInline plus source metadata for token inventory.
func ParseInlineEntries(s string) []Entry {
	var out []Entry
	for _, pair := range strings.Split(s, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		node, tok, ok := strings.Cut(pair, "=")
		node = strings.TrimSpace(node)
		tok = strings.TrimSpace(tok)
		if !ok || node == "" || tok == "" {
			continue
		}
		out = append(out, Entry{NodeID: node, Token: tok, Source: SourceInline})
	}
	return out
}

func entriesFromMap(m map[string]string, source string) []Entry {
	out := make([]Entry, 0, len(m))
	for node, tok := range m {
		out = append(out, Entry{NodeID: node, Token: tok, Source: source})
	}
	return out
}

func normalizeEntry(nodeID, token, source string) (string, string, string, error) {
	node := strings.TrimSpace(nodeID)
	tok := strings.TrimSpace(token)
	if node == "" || tok == "" {
		return "", "", "", ErrInvalidEntry
	}
	src := strings.TrimSpace(source)
	if src == "" {
		src = SourceMemory
	}
	return node, tok, src, nil
}

func fingerprint(token string) string {
	sum := sha256.Sum256([]byte(token))
	return "sha256:" + hex.EncodeToString(sum[:])[:12]
}
