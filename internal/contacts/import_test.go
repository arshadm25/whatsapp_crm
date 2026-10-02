package contacts

import "testing"

func TestNormalizeNumber(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"98765 43210", "919876543210", true},
		{"098765-43210", "919876543210", true},
		{"+91 98765 43210", "919876543210", true},
		{"0044 7700 900123", "447700900123", true},
		{"+1 (415) 555-0100", "14155550100", true},
		{"919876543210", "919876543210", true},
		{"12345", "12345", false},
		{"abc", "", false},
	}
	for _, c := range cases {
		got, ok := NormalizeNumber(c.in, "91")
		if got != c.want || ok != c.ok {
			t.Errorf("NormalizeNumber(%q) = %q %v, want %q %v", c.in, got, ok, c.want, c.ok)
		}
	}
}
