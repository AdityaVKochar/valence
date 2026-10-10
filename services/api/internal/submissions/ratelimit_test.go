package submissions

import (
	"testing"
	"time"
)

func TestRateLimiter(t *testing.T) {
	now := time.Unix(0, 0)
	l := NewRateLimiter(5*time.Second, 1)
	l.now = func() time.Time { return now }

	if ok, _ := l.Allow(1); !ok {
		t.Fatal("first submission should pass")
	}
	ok, wait := l.Allow(1)
	if ok || wait != 5*time.Second {
		t.Fatalf("second submission: ok=%v wait=%v, want blocked for 5s", ok, wait)
	}
	if ok, _ := l.Allow(2); !ok {
		t.Fatal("another user should not be limited")
	}
	now = now.Add(3 * time.Second)
	if ok, wait := l.Allow(1); ok || wait != 2*time.Second {
		t.Fatalf("after 3s: ok=%v wait=%v, want blocked for 2s", ok, wait)
	}
	now = now.Add(2 * time.Second)
	if ok, _ := l.Allow(1); !ok {
		t.Fatal("after the cooldown the submission should pass")
	}
}

func TestRateLimiterBurstAndDisabled(t *testing.T) {
	now := time.Unix(0, 0)
	l := NewRateLimiter(time.Second, 3)
	l.now = func() time.Time { return now }
	for i := range 3 {
		if ok, _ := l.Allow(1); !ok {
			t.Fatalf("burst submission %d blocked", i)
		}
	}
	if ok, _ := l.Allow(1); ok {
		t.Fatal("fourth submission should be blocked")
	}
	off := NewRateLimiter(0, 1)
	for range 10 {
		if ok, _ := off.Allow(1); !ok {
			t.Fatal("a zero cooldown disables the limit")
		}
	}
}
