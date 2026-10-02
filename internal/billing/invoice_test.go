package billing

import (
	"testing"
	"time"
)

func TestSplitGST(t *testing.T) {
	cases := []struct {
		total int64
		same  bool
		want  Tax
	}{
		{118000, true, Tax{Taxable: 100000, CGST: 9000, SGST: 9000}},
		{118000, false, Tax{Taxable: 100000, IGST: 18000}},
		{100000, true, Tax{Taxable: 84746, CGST: 7627, SGST: 7627}}, // 15254 of tax: SGST takes the odd paisa
		{99999, false, Tax{Taxable: 84745, IGST: 15254}},
	}
	for _, c := range cases {
		got := SplitGST(c.total, 1800, c.same)
		if got.Taxable+got.CGST+got.SGST+got.IGST != c.total {
			t.Errorf("SplitGST(%d) = %+v does not add up", c.total, got)
		}
		if c.total == 100000 && c.same {
			if got.Taxable != 84746 || got.CGST+got.SGST != 15254 || got.SGST-got.CGST > 1 {
				t.Errorf("SplitGST(100000) = %+v", got)
			}
			continue
		}
		if got != c.want {
			t.Errorf("SplitGST(%d, %v) = %+v, want %+v", c.total, c.same, got, c.want)
		}
	}
}

func TestFinancialYear(t *testing.T) {
	ist := time.FixedZone("IST", 5*3600+1800)
	for in, want := range map[time.Time]string{
		time.Date(2026, 4, 1, 0, 0, 0, 0, ist):        "2026-27",
		time.Date(2027, 3, 31, 23, 0, 0, 0, ist):      "2026-27",
		time.Date(2027, 4, 1, 0, 0, 0, 0, ist):        "2027-28",
		time.Date(2026, 3, 31, 20, 0, 0, 0, time.UTC): "2026-27", // already 1 April in India
		time.Date(2099, 12, 1, 0, 0, 0, 0, ist):       "2099-00",
	} {
		if got := FinancialYear(in); got != want {
			t.Errorf("FinancialYear(%v) = %s, want %s", in, got, want)
		}
	}
}

func TestINR(t *testing.T) {
	for in, want := range map[int64]string{0: "0.00", 5: "0.05", 99900: "999.00", 118000: "1,180.00", 123456789: "12,34,567.89", -1050: "-10.50"} {
		if got := inr(in); got != want {
			t.Errorf("inr(%d) = %s, want %s", in, got, want)
		}
	}
}

func TestValidStateCode(t *testing.T) {
	for c, want := range map[string]bool{"32": true, "01": true, "38": true, "97": true, "00": false, "39": false, "3": false, "ab": false} {
		if ValidStateCode(c) != want {
			t.Errorf("ValidStateCode(%q) = %v", c, !want)
		}
	}
}
