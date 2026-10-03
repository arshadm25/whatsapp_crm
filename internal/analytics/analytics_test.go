package analytics

import (
	"testing"
	"time"
)

func TestCountry(t *testing.T) {
	for wa, want := range map[string]string{"919876543210": "IN", "971501234567": "AE", "14155550100": "US", "99912345": "other"} {
		if got := Country(wa); got != want {
			t.Errorf("Country(%s) = %s, want %s", wa, got, want)
		}
	}
}

func TestCost(t *testing.T) {
	if p, ok := Cost("IN", "marketing", 10); !ok || p != 785 {
		t.Errorf("10 marketing = %d %v", p, ok)
	}
	if p, ok := Cost("IN", "service", 10); !ok || p != 0 {
		t.Errorf("service = %d %v", p, ok)
	}
	if _, ok := Cost("other", "utility", 1); ok {
		t.Error("unknown country priced")
	}
}

func TestDateRange(t *testing.T) {
	today := time.Date(2026, 10, 2, 23, 30, 0, 0, india)
	from, to, err := dateRange("", "", today)
	if err != nil || from.Format(time.DateOnly) != "2026-09-03" || to.Format(time.DateOnly) != "2026-10-02" {
		t.Fatalf("default = %v %v %v", from, to, err)
	}
	from, _, _ = dateRange("", "2026-06-30", today)
	if from.Format(time.DateOnly) != "2026-06-01" {
		t.Fatalf("30 days before to = %v", from)
	}
	for _, bad := range [][2]string{{"2026-13-01", ""}, {"2026-10-05", "2026-10-01"}, {"2025-01-01", "2026-10-01"}} {
		if _, _, err := dateRange(bad[0], bad[1], today); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}

func TestMedian(t *testing.T) {
	if median(nil) != nil {
		t.Fatal("median of nothing")
	}
	for _, c := range []struct {
		in   []float64
		want float64
	}{{[]float64{5}, 5}, {[]float64{9, 1, 5}, 5}, {[]float64{4, 1, 3, 2}, 2.5}} {
		if got := *median(c.in); got != c.want {
			t.Errorf("median(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}
