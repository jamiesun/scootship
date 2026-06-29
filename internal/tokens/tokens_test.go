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
