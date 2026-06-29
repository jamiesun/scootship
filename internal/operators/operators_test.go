package operators

import (
	"errors"
	"testing"
	"time"
)

func TestBootstrapAuthenticateAndUpdate(t *testing.T) {
	now := time.Unix(100, 0)
	st, err := Open("", "Admin", "secret", now)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Configured() || st.Count() != 1 {
		t.Fatalf("bootstrap failed: configured=%v count=%d", st.Configured(), st.Count())
	}
	if _, ok := st.Authenticate("admin", "wrong", now); ok {
		t.Fatal("wrong password authenticated")
	}
	snap, ok := st.Authenticate("admin", "secret", now)
	if !ok {
		t.Fatal("bootstrap password did not authenticate")
	}
	if snap.Username != "admin" || snap.LastLoginMS != now.UnixMilli() {
		t.Fatalf("unexpected snapshot: %+v", snap)
	}
	if err := st.UpdateProfile("admin", "Root Operator", "root@example.test", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	snap, ok = st.Get("admin")
	if !ok || snap.DisplayName != "Root Operator" || snap.Email != "root@example.test" {
		t.Fatalf("profile not updated: %+v", snap)
	}
}

func TestCreateAndChangePassword(t *testing.T) {
	st, err := Open("", "admin", "secret", time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Create("alice", "Alice", "alice@example.test", "pw1", time.Unix(2, 0)); err != nil {
		t.Fatal(err)
	}
	if err := st.Create("alice", "Alice", "", "pw1", time.Unix(2, 0)); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate err = %v, want ErrDuplicate", err)
	}
	if _, ok := st.Authenticate("alice", "pw1", time.Unix(3, 0)); !ok {
		t.Fatal("created operator did not authenticate")
	}
	if err := st.ChangePassword("alice", "bad", "pw2", time.Unix(4, 0)); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("bad current password err = %v, want ErrBadCredentials", err)
	}
	if err := st.ChangePassword("alice", "pw1", "pw2", time.Unix(4, 0)); err != nil {
		t.Fatal(err)
	}
	if _, ok := st.Authenticate("alice", "pw1", time.Unix(5, 0)); ok {
		t.Fatal("old password still authenticates")
	}
	if _, ok := st.Authenticate("alice", "pw2", time.Unix(5, 0)); !ok {
		t.Fatal("new password did not authenticate")
	}
}

func TestPersistsOperators(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open(dir, "admin", "secret", time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	st, err := Open(dir, "ignored", "ignored", time.Unix(2, 0))
	if err != nil {
		t.Fatal(err)
	}
	if st.Count() != 1 {
		t.Fatalf("reopen count=%d, want 1", st.Count())
	}
	if _, ok := st.Authenticate("admin", "secret", time.Unix(3, 0)); !ok {
		t.Fatal("persisted operator did not authenticate")
	}
	if _, ok := st.Authenticate("ignored", "ignored", time.Unix(3, 0)); ok {
		t.Fatal("bootstrap should not overwrite existing store")
	}
}
