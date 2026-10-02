package devportal

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNewKey(t *testing.T) {
	k1, p1, h1 := newKey()
	k2, _, _ := newKey()
	if k1 == k2 || !strings.HasPrefix(k1, p1) || len(p1) != len("eco_live_")+4 || string(h1) != string(hashKey(k1)) {
		t.Fatalf("keys %q %q prefix %q", k1, k2, p1)
	}
}

func TestLimiter(t *testing.T) {
	now := time.Unix(0, 0)
	l := NewLimiter(2)
	l.now = func() time.Time { return now }
	k := uuid.New()
	for i := 0; i < 2; i++ {
		if ok, _, _ := l.Take(k); !ok {
			t.Fatalf("request %d refused", i)
		}
	}
	ok, _, wait := l.Take(k)
	if ok || wait != 500*time.Millisecond {
		t.Fatalf("third request: ok %v wait %v", ok, wait)
	}
	if ok, _, _ := l.Take(uuid.New()); !ok {
		t.Fatal("another key shares the bucket")
	}
	now = now.Add(500 * time.Millisecond)
	if ok, _, _ := l.Take(k); !ok {
		t.Fatal("token not refilled")
	}
}
