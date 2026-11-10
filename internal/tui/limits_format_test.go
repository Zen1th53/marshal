package tui

import (
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/usage"
)

func TestLimitBar(t *testing.T) {
	for _, tc := range []struct {
		pct     int
		unicode bool
		want    string
	}{
		{42, true, "▓▓░"},
		{0, true, "░░░"},
		{1, true, "▓░░"},
		{100, false, "==="},
		{100, true, "▓▓▓"},
		{33, true, "▓░░"},
		{34, true, "▓▓░"},
		{67, true, "▓▓▓"},
		{-5, true, "░░░"},
		{150, true, "▓▓▓"},
		{50, false, "==-"},
	} {
		if got := limitBar(tc.pct, tc.unicode); got != tc.want {
			t.Errorf("limitBar(%d, %v) = %q, want %q", tc.pct, tc.unicode, got, tc.want)
		}
	}
}

func TestResetIn(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "1m"},
		{45 * time.Minute, "45m"},
		{59*time.Minute + 30*time.Second, "1h"},
		{3*time.Hour + 20*time.Minute, "3h"},
		{98 * time.Hour, "4d"},
		{0, "now"},
		{-time.Hour, "now"},
		{59 * time.Minute, "59m"},
		{60 * time.Minute, "1h"},
		{24 * time.Hour, "1d"},
	} {
		if got := resetIn(tc.d); got != tc.want {
			t.Errorf("resetIn(%s) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

func TestLimitFigure(t *testing.T) {
	now := time.Date(2026, 11, 8, 18, 0, 0, 0, time.UTC)
	fresh := usage.Quota{Provider: "claude", Known: true, Windows: []usage.Window{
		{Label: "5h", LeftPercent: 88, ResetsAt: now.Add(2 * time.Hour)},
		{Label: "wk", LeftPercent: 58, ResetsAt: now.Add(98 * time.Hour)},
	}}
	if s, pct, ok := limitFigure(fresh, now); !ok || s != "42% ·4d" || pct != 42 {
		t.Fatalf("fresh = %q %d %v", s, pct, ok)
	}
	old := fresh
	old.Stale = true
	if s, pct, ok := limitFigure(old, now); !ok || s != "~42% ·4d" || pct != 42 {
		t.Fatalf("old = %q %d %v", s, pct, ok)
	}
	if _, _, ok := limitFigure(usage.Quota{Provider: "claude", Reason: usage.ReasonNoFile}, now); ok {
		t.Fatal("unknown quota must give ok=false")
	}
	if _, _, ok := limitFigure(usage.Quota{Provider: "claude", Known: true, Windows: []usage.Window{{Label: "5h"}}}, now); ok {
		t.Fatal("no known window must give ok=false")
	}
}

func TestLimitReason(t *testing.T) {
	for _, tc := range []struct {
		reason string
		want   string
	}{
		{usage.NotExposed, "claude does not write its usage limits where Marshal can read them"},
		{usage.ReasonNoFile, "claude has not reported its usage limits on this machine yet"},
		{usage.ReasonNotReported, "claude has not reported its usage limits on this machine yet"},
		{usage.ReasonStale, "claude's last usage report is more than a day old"},
		{usage.ReasonReset, "the limit reset since claude last reported it"},
		{usage.ReasonMalformed, "claude's usage report could not be read"},
		{usage.ReasonMissing, "claude's usage report could not be read"},
		{usage.ReasonOutOfRange, "claude's usage report could not be read"},
		{"some other code", "claude's usage report could not be read"},
	} {
		q := usage.Quota{Provider: "claude", Reason: tc.reason}
		if got := limitReason(q); got != tc.want {
			t.Errorf("limitReason(%q) = %q, want %q", tc.reason, got, tc.want)
		}
	}
}
