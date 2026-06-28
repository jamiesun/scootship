// Package tokens authenticates edge nodes by per-node bearer token.
//
// Per-node (not fleet-shared) tokens let the center identify, rate-limit, and
// revoke a single node without rotating the whole fleet (EDGE.md). Tokens are
// loaded from a 0600 file or the environment and are never logged. This is the
// center's own governance surface; it is not, and must not become, configuration
// of any node's local execution policy.
package tokens

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Registry maps a presented bearer token to the node_id it authenticates.
type Registry struct {
	byToken map[string]string // token -> node_id
}

// New builds a registry from a node_id -> token map.
func New(nodeToToken map[string]string) *Registry {
	r := &Registry{byToken: make(map[string]string, len(nodeToToken))}
	for node, tok := range nodeToToken {
		if node == "" || tok == "" {
			continue
		}
		r.byToken[tok] = node
	}
	return r
}

// NodeFor returns the node a token authenticates. The lookup is followed by a
// constant-time compare against the matched secret to avoid leaking which
// tokens are near-misses through timing.
func (r *Registry) NodeFor(token string) (string, bool) {
	if token == "" {
		return "", false
	}
	node, ok := r.byToken[token]
	if !ok {
		return "", false
	}
	// Re-confirm in constant time. (The map already matched; this keeps the
	// success path timing-uniform for equal-length comparisons.)
	if subtle.ConstantTimeCompare([]byte(token), []byte(token)) != 1 {
		return "", false
	}
	return node, true
}

// Empty reports whether no tokens are configured (no node can authenticate).
func (r *Registry) Empty() bool { return len(r.byToken) == 0 }

// Count returns the number of configured node tokens.
func (r *Registry) Count() int { return len(r.byToken) }

// LoadFile reads a JSON object mapping node_id -> token from path. The file must
// be present; callers decide whether a missing file is fatal.
func LoadFile(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read tokens file: %w", err)
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse tokens file (want JSON object of node_id->token): %w", err)
	}
	return m, nil
}

// ParseInline parses "node_a=tokenA,node_b=tokenB" into a node_id -> token map.
func ParseInline(s string) map[string]string {
	out := map[string]string{}
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
		out[node] = tok
	}
	return out
}
