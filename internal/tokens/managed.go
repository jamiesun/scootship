package tokens

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const managedTokensFile = "managed_node_tokens.json"

// ManagedState is the center-owned token lifecycle overlay. Tokens are raw
// bearer secrets and must only be written to a private server-side file.
type ManagedState struct {
	Tokens  map[string]string `json:"tokens,omitempty"`
	Revoked []string          `json:"revoked,omitempty"`
}

// ManagedStore persists the center-owned lifecycle overlay in dataDir. An empty
// dataDir keeps lifecycle changes in memory for tests.
type ManagedStore struct {
	mu   sync.Mutex
	path string
}

func NewManagedStore(dataDir string) *ManagedStore {
	if dataDir == "" {
		return &ManagedStore{}
	}
	return &ManagedStore{path: filepath.Join(dataDir, managedTokensFile)}
}

func ManagedPath(dataDir string) string {
	if dataDir == "" {
		return ""
	}
	return filepath.Join(dataDir, managedTokensFile)
}

func (s *ManagedStore) Load() (ManagedState, error) {
	if s == nil {
		return ManagedState{}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path == "" {
		return ManagedState{}, nil
	}
	if _, err := os.Stat(s.path); err != nil {
		if os.IsNotExist(err) {
			return ManagedState{}, nil
		}
		return ManagedState{}, fmt.Errorf("stat managed token state: %w", err)
	}
	if err := checkPrivateFile(s.path); err != nil {
		return ManagedState{}, fmt.Errorf("managed token state: %w", err)
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return ManagedState{}, fmt.Errorf("read managed token state: %w", err)
	}
	var state ManagedState
	if err := json.Unmarshal(data, &state); err != nil {
		return ManagedState{}, fmt.Errorf("parse managed token state: %w", err)
	}
	return normalizeManagedState(state), nil
}

func (s *ManagedStore) Save(state ManagedState) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create token state dir: %w", err)
	}
	state = normalizeManagedState(state)
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode managed token state: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write managed token state: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("replace managed token state: %w", err)
	}
	return nil
}

// ApplyManagedState overlays center-owned lifecycle state onto static entries.
// Revoked nodes are suppressed first; managed tokens are appended after static
// entries so normal last-entry-wins semantics rotate those nodes.
func ApplyManagedState(entries []Entry, state ManagedState) []Entry {
	state = normalizeManagedState(state)
	revoked := map[string]bool{}
	for _, node := range state.Revoked {
		if _, hasManagedToken := state.Tokens[node]; !hasManagedToken {
			revoked[node] = true
		}
	}
	out := make([]Entry, 0, len(entries)+len(state.Tokens))
	for _, e := range entries {
		node := strings.TrimSpace(e.NodeID)
		if node == "" || revoked[node] {
			continue
		}
		out = append(out, e)
	}
	nodes := make([]string, 0, len(state.Tokens))
	for node := range state.Tokens {
		nodes = append(nodes, node)
	}
	sort.Strings(nodes)
	for _, node := range nodes {
		out = append(out, Entry{NodeID: node, Token: state.Tokens[node], Source: SourceManaged})
	}
	return out
}

func normalizeManagedState(state ManagedState) ManagedState {
	tokens := map[string]string{}
	for node, tok := range state.Tokens {
		node = strings.TrimSpace(node)
		tok = strings.TrimSpace(tok)
		if node == "" || tok == "" {
			continue
		}
		tokens[node] = tok
	}
	revokedSet := map[string]bool{}
	for _, node := range state.Revoked {
		node = strings.TrimSpace(node)
		if node == "" {
			continue
		}
		if _, hasToken := tokens[node]; hasToken {
			continue
		}
		revokedSet[node] = true
	}
	revoked := make([]string, 0, len(revokedSet))
	for node := range revokedSet {
		revoked = append(revoked, node)
	}
	sort.Strings(revoked)
	if len(tokens) == 0 {
		tokens = nil
	}
	return ManagedState{Tokens: tokens, Revoked: revoked}
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func removeString(items []string, remove string) []string {
	out := items[:0]
	for _, item := range items {
		if item != remove {
			out = append(out, item)
		}
	}
	return out
}
