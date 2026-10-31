package billing

import (
	"testing"
	"time"
)

func TestUnusedShare(t *testing.T) {
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 30)
	for _, c := range []struct {
		now  time.Time
		want int64
	}{
		{start.AddDate(0, 0, 10), 200000}, {start, 300000}, {start.AddDate(0, 0, -1), 300000},
		{end, 0}, {end.AddDate(0, 0, 1), 0}, {start.AddDate(0, 0, 29), 10000},
	} {
		if got := UnusedShare(300000, start, end, c.now); got != c.want {
			t.Errorf("UnusedShare at %s = %d, want %d", c.now, got, c.want)
		}
	}
}
