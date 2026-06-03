package main

import (
	"strings"
	"testing"

	"stock-tui/internal/indicator"
)

func TestRunRequiresCode(t *testing.T) {
	err := run(nil)
	if err == nil {
		t.Fatal("run(nil) error = nil, want usage error")
	}
}

func TestRunRejectsInvalidBarsBeforeNetwork(t *testing.T) {
	err := run([]string{"-n", "0", "600900"})
	if err == nil {
		t.Fatal("run(-n 0) error = nil, want validation error")
	}
}

func TestRunRejectsMalformedCodeBeforeNetwork(t *testing.T) {
	err := run([]string{"abc"})
	if err == nil {
		t.Fatal("run(malformed) error = nil, want validation error")
	}
	if !strings.Contains(err.Error(), "invalid code") {
		t.Fatalf("run(malformed) error = %v, want invalid code", err)
	}
}

func TestScoreLabelUsesTechnicalState(t *testing.T) {
	cases := []struct {
		score int
		want  string
	}{
		{100, "技术极强"},
		{85, "技术极强"},
		{84, "技术偏强"},
		{70, "技术偏强"},
		{69, "技术略偏强"},
		{55, "技术略偏强"},
		{54, "技术中性/方向不明"},
		{45, "技术中性/方向不明"},
		{44, "技术略偏弱"},
		{31, "技术略偏弱"},
		{30, "技术偏弱"},
		{16, "技术偏弱"},
		{15, "技术极弱"},
		{0, "技术极弱"},
	}

	for _, tc := range cases {
		if got := scoreLabel(tc.score); got != tc.want {
			t.Fatalf("scoreLabel(%d) = %q, want %q", tc.score, got, tc.want)
		}
	}
}

func TestPerformanceUsesSignalNames(t *testing.T) {
	perfs := performance(nil, nil, nil, nil, nil)
	if len(perfs) < 12 {
		t.Fatalf("performance() returned %d rows, want at least 12", len(perfs))
	}

	if perfs[10].Name != "TD见底Countdown" {
		t.Fatalf("TD bottom signal name = %q, want TD见底Countdown", perfs[10].Name)
	}
	if perfs[11].Name != "TD见顶Countdown" {
		t.Fatalf("TD top signal name = %q, want TD见顶Countdown", perfs[11].Name)
	}
}

func TestTDSignalTextUsesTechnicalState(t *testing.T) {
	if got := tdSignalText(indicator.TDBuy); got != "见底" {
		t.Fatalf("tdSignalText(TDBuy) = %q, want 见底", got)
	}
	if got := tdSignalText(indicator.TDSell); got != "见顶" {
		t.Fatalf("tdSignalText(TDSell) = %q, want 见顶", got)
	}
}

func TestTDShortUsesTechnicalDirection(t *testing.T) {
	bottom := tdShort(indicator.TD{SetupSignal: indicator.TDBuy, SetupCount: 9, SetupPerfected: true})
	if bottom != "S底9*" {
		t.Fatalf("tdShort(bottom setup) = %q, want S底9*", bottom)
	}

	top := tdShort(indicator.TD{CountdownSignal: indicator.TDSell, CountdownCount: 13})
	if top != "C顶13" {
		t.Fatalf("tdShort(top countdown) = %q, want C顶13", top)
	}
}
