package tokens

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRegistryAuthenticatesPerNodeToken(t *testing.T) {
	r := New(map[string]string{
		"n-1": "secret-1",
		"n-2": "secret-2",
		"":    "ignored-node",
		"n-3": "",
	})

	if r.Count() != 2 {
		t.Fatalf("Count() = %d, want 2", r.Count())
	}
	if r.Empty() {
		t.Fatal("registry should not be empty")
	}
	now := time.Unix(123, 0)
	if node, ok := r.NodeForAt("secret-1", now); !ok || node != "n-1" {
		t.Fatalf("NodeFor(secret-1) = %q, %v; want n-1, true", node, ok)
	}
	if node, ok := r.NodeFor("secret-2"); !ok || node != "n-2" {
		t.Fatalf("NodeFor(secret-2) = %q, %v; want n-2, true", node, ok)
	}
	if node, ok := r.NodeFor("nope"); ok || node != "" {
		t.Fatalf("NodeFor(nope) = %q, %v; want empty, false", node, ok)
	}

	snaps := r.Snapshots()
	if len(snaps) != 2 {
		t.Fatalf("Snapshots len = %d, want 2", len(snaps))
	}
	if snaps[0].NodeID != "n-1" || snaps[0].Source != SourceMemory {
		t.Fatalf("unexpected first snapshot: %+v", snaps[0])
	}
	if snaps[0].Fingerprint == "" || strings.Contains(snaps[0].Fingerprint, "secret") {
		t.Fatalf("unsafe fingerprint: %q", snaps[0].Fingerprint)
	}
	if snaps[0].LastAuthenticatedMS != now.UnixMilli() {
		t.Fatalf("LastAuthenticatedMS = %d, want %d", snaps[0].LastAuthenticatedMS, now.UnixMilli())
	}
}

func TestParseInline(t *testing.T) {
	got := ParseInline(" n-1 = secret-1 ,bad,n-2=secret-2,empty=,=missing-node ")
	if len(got) != 2 {
		t.Fatalf("len(ParseInline) = %d, want 2 (%v)", len(got), got)
	}
	if got["n-1"] != "secret-1" || got["n-2"] != "secret-2" {
		t.Fatalf("unexpected parsed tokens: %v", got)
	}

	entries := ParseInlineEntries("n-1=secret-1")
	if len(entries) != 1 || entries[0].Source != SourceInline {
		t.Fatalf("unexpected inline entries: %+v", entries)
	}
}

func TestNewEntriesLastNodeWins(t *testing.T) {
	r := NewEntries([]Entry{
		{NodeID: "n-1", Token: "old", Source: SourceFile},
		{NodeID: "n-1", Token: "new", Source: SourceInline},
	})
	if node, ok := r.NodeFor("old"); ok || node != "" {
		t.Fatalf("old token should have been replaced, got %q %v", node, ok)
	}
	if node, ok := r.NodeFor("new"); !ok || node != "n-1" {
		t.Fatalf("new token = %q %v, want n-1 true", node, ok)
	}
	snaps := r.Snapshots()
	if len(snaps) != 1 || snaps[0].Source != SourceInline {
		t.Fatalf("unexpected snapshots after override: %+v", snaps)
	}
}

func TestManagedStateOverridesAndRevokesStaticTokens(t *testing.T) {
	store := NewManagedStore(t.TempDir())
	if err := store.Save(ManagedState{
		Tokens:  map[string]string{"n-2": "managed-token-0002"},
		Revoked: []string{"n-1"},
	}); err != nil {
		t.Fatal(err)
	}
	r, err := NewEntriesWithManaged([]Entry{
		{NodeID: "n-1", Token: "static-token-0001", Source: SourceFile},
		{NodeID: "n-2", Token: "old-static-0002", Source: SourceInline},
	}, store)
	if err != nil {
		t.Fatal(err)
	}
	if node, ok := r.NodeFor("static-token-0001"); ok || node != "" {
		t.Fatalf("revoked static token authenticated as %q", node)
	}
	if node, ok := r.NodeFor("old-static-0002"); ok || node != "" {
		t.Fatalf("overridden static token authenticated as %q", node)
	}
	if node, ok := r.NodeFor("managed-token-0002"); !ok || node != "n-2" {
		t.Fatalf("managed token = %q %v, want n-2 true", node, ok)
	}
	snaps := r.Snapshots()
	if len(snaps) != 1 || snaps[0].NodeID != "n-2" || snaps[0].Source != SourceManaged {
		t.Fatalf("unexpected managed snapshots: %+v", snaps)
	}
}

func TestManagedLifecyclePersistsPrivateState(t *testing.T) {
	dir := t.TempDir()
	store := NewManagedStore(dir)
	r, err := NewEntriesWithManaged([]Entry{{NodeID: "n-1", Token: "static-token-0001", Source: SourceFile}}, store)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.UpsertManaged("n-1", "rotated-token-0001"); err != nil {
		t.Fatal(err)
	}
	if node, ok := r.NodeFor("static-token-0001"); ok || node != "" {
		t.Fatalf("old static token should be rotated away, got %q %v", node, ok)
	}
	if node, ok := r.NodeFor("rotated-token-0001"); !ok || node != "n-1" {
		t.Fatalf("rotated token = %q %v, want n-1 true", node, ok)
	}

	path := ManagedPath(dir)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("managed token state mode = %04o, want 0600", got)
	}

	if err := r.RevokeManaged("n-1"); err != nil {
		t.Fatal(err)
	}
	if node, ok := r.NodeFor("rotated-token-0001"); ok || node != "" {
		t.Fatalf("revoked token authenticated as %q", node)
	}
	reloaded, err := NewEntriesWithManaged([]Entry{{NodeID: "n-1", Token: "static-token-0001", Source: SourceFile}}, store)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Count() != 0 {
		t.Fatalf("revocation should suppress static token on restart, got %d tokens", reloaded.Count())
	}
}

func TestManagedLifecycleRejectsDuplicateTokenForAnotherNode(t *testing.T) {
	r := NewEntries([]Entry{
		{NodeID: "n-1", Token: "shared-token-0001", Source: SourceFile},
	})
	if err := r.UpsertManaged("n-2", "shared-token-0001"); err != ErrDuplicateToken {
		t.Fatalf("UpsertManaged duplicate err = %v, want ErrDuplicateToken", err)
	}
	if node, ok := r.NodeFor("shared-token-0001"); !ok || node != "n-1" {
		t.Fatalf("duplicate attempt changed ownership: %q %v", node, ok)
	}
}

func TestLoadFileRequiresPrivatePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.json")
	if err := os.WriteFile(path, []byte(`{"n-1":"secret"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadFile(path)
	if err == nil || !strings.Contains(err.Error(), "insecure permissions") {
		t.Fatalf("LoadFile with 0644 err = %v, want insecure permissions", err)
	}

	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile with 0600: %v", err)
	}
	if got["n-1"] != "secret" {
		t.Fatalf("unexpected tokens: %v", got)
	}
}

func TestLoadFileRejectsDirectories(t *testing.T) {
	_, err := LoadFile(t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("LoadFile(directory) err = %v, want regular file error", err)
	}
}
