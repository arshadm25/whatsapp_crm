package numbers

import (
	"testing"
	"time"

	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
)

func TestDailyLimit(t *testing.T) {
	s := func(v string) *string { return &v }
	for tier, want := range map[*string]int32{nil: 250, s("TIER_2K"): 2000, s("tier_10k"): 10000, s("TIER_UNLIMITED"): -1, s("TIER_NEW"): 250} {
		if got := DailyLimit(tier); got != want {
			t.Errorf("DailyLimit(%v) = %d, want %d", tier, got, want)
		}
	}
}

func TestQualityDropped(t *testing.T) {
	now := time.Now()
	q := func(v dbq.QualityRating) *dbq.QualityRating { return &v }
	at := func(d time.Duration) *time.Time { v := now.Add(-d); return &v }
	for _, c := range []struct {
		prev *dbq.QualityRating
		cur  dbq.QualityRating
		at   *time.Time
		want bool
	}{
		{q("green"), "yellow", at(time.Hour), true},
		{q("yellow"), "red", at(6 * 24 * time.Hour), true},
		{q("green"), "red", at(8 * 24 * time.Hour), false},
		{q("red"), "green", at(time.Hour), false},
		{q("unknown"), "red", at(time.Hour), false},
		{nil, "red", nil, false},
	} {
		p := dbq.PhoneNumber{PreviousQualityRating: c.prev, QualityRating: c.cur, QualityChangedAt: c.at}
		if got := qualityDropped(p, now); got != c.want {
			t.Errorf("%v -> %v: dropped = %v, want %v", c.prev, c.cur, got, c.want)
		}
	}
}
