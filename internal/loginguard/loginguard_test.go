package loginguard

import (
	"testing"
	"time"
)

func TestLockoutAfterMaxFails(t *testing.T) {
	g := New(Options{MaxFails: 3, Window: time.Minute, Lockout: time.Minute})
	now := time.Unix(1000, 0)

	// First two failures stay allowed.
	for i := 1; i <= 2; i++ {
		d := g.Fail("1.2.3.4", now)
		if !d.Allowed {
			t.Fatalf("failure %d should still be allowed", i)
		}
	}
	// Third failure trips the lockout.
	d := g.Fail("1.2.3.4", now)
	if d.Allowed {
		t.Fatal("third failure should lock out")
	}
	if d.RetryAfter != time.Minute {
		t.Fatalf("retry_after=%v want 1m", d.RetryAfter)
	}

	// A subsequent check during the lockout is denied.
	if c := g.Check("1.2.3.4", now.Add(10*time.Second)); c.Allowed {
		t.Fatal("check during lockout should be denied")
	}
	// A different key is unaffected.
	if c := g.Check("5.6.7.8", now); !c.Allowed {
		t.Fatal("unrelated key should be allowed")
	}
}

func TestLockoutExpires(t *testing.T) {
	g := New(Options{MaxFails: 2, Window: time.Minute, Lockout: time.Minute})
	now := time.Unix(1000, 0)
	g.Fail("ip", now)
	d := g.Fail("ip", now)
	if d.Allowed {
		t.Fatal("should be locked after 2 fails")
	}
	// After the lockout passes, the key may try again.
	later := now.Add(2 * time.Minute)
	if c := g.Check("ip", later); !c.Allowed {
		t.Fatal("should be allowed after lockout expires")
	}
	// And the counter starts fresh: a single failure does not immediately relock.
	if d := g.Fail("ip", later); !d.Allowed {
		t.Fatal("first failure after lockout expiry should be allowed")
	}
}

func TestWindowDecay(t *testing.T) {
	g := New(Options{MaxFails: 3, Window: time.Minute, Lockout: time.Minute})
	now := time.Unix(1000, 0)
	g.Fail("ip", now)
	g.Fail("ip", now)
	// Two minutes later the window has decayed; this counts as the first failure.
	d := g.Fail("ip", now.Add(2*time.Minute))
	if !d.Allowed || d.Remaining != 2 {
		t.Fatalf("expected fresh window (allowed, remaining 2), got %+v", d)
	}
}

func TestResetClearsKey(t *testing.T) {
	g := New(Options{MaxFails: 2})
	now := time.Unix(1000, 0)
	g.Fail("ip", now)
	g.Reset("ip")
	if c := g.Check("ip", now); !c.Allowed || c.Remaining != 2 {
		t.Fatalf("reset should clear state, got %+v", c)
	}
}
