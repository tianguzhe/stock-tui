package api

import (
	"testing"
)

func TestCodeToSecid(t *testing.T) {
	cases := []struct {
		code string
		want string
		err  bool
	}{
		{"sh600522", "1.600522", false},
		{"sz000001", "0.000001", false},
		{"bj920819", "0.920819", false},
		{"usAAPL", "", true},
		{"s", "", true},
	}
	for _, tc := range cases {
		got, err := CodeToSecid(tc.code)
		if tc.err {
			if err == nil {
				t.Errorf("CodeToSecid(%q) = %q, nil; want error", tc.code, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("CodeToSecid(%q) error: %v", tc.code, err)
			continue
		}
		if got != tc.want {
			t.Errorf("CodeToSecid(%q) = %q, want %q", tc.code, got, tc.want)
		}
	}
}

func TestCodeToTDXMarket(t *testing.T) {
	cases := []struct {
		code    string
		wantRaw string
		err     bool
	}{
		{"sh600522", "600522", false},
		{"sz000001", "000001", false},
		{"bj920819", "920819", false},
		{"usAAPL", "", true},
	}
	for _, tc := range cases {
		_, raw, err := CodeToTDXMarket(tc.code)
		if tc.err {
			if err == nil {
				t.Errorf("CodeToTDXMarket(%q) want error", tc.code)
			}
			continue
		}
		if err != nil {
			t.Errorf("CodeToTDXMarket(%q) error: %v", tc.code, err)
			continue
		}
		if raw != tc.wantRaw {
			t.Errorf("CodeToTDXMarket(%q) raw = %q, want %q", tc.code, raw, tc.wantRaw)
		}
	}
}

func TestParseEMFloat(t *testing.T) {
	if got := parseEMFloat("123.45"); got != 123.45 {
		t.Errorf("parseEMFloat(%q) = %v, want 123.45", "123.45", got)
	}
	if got := parseEMFloat("0.47"); got != 0.47 {
		t.Errorf("parseEMFloat(%q) = %v, want 0.47", "0.47", got)
	}
	if got := parseEMFloat(""); got != 0 {
		t.Errorf("parseEMFloat(%q) = %v, want 0", "", got)
	}
	if got := parseEMFloat("abc"); got != 0 {
		t.Errorf("parseEMFloat(%q) = %v, want 0", "abc", got)
	}
}
