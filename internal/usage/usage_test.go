package usage

import (
	"path/filepath"
	"testing"
	"time"
)

func TestReadersAndNotExposed(t *testing.T) {
	dir := t.TempDir()
	place(t, dir, "2026/11/08", "rollout-a.jsonl", fixture(t, "fresh.jsonl"), now.Add(-time.Minute))
	if q := ReadCodex(now, dir); !q.Known || q.Provider != "codex" {
		t.Fatalf("codex = %+v", q)
	}
	// With no Claude file, claude is unknown with ReasonNoFile, not a number.
	if cl := ReadClaude(now, "", ""); cl.Known || cl.Reason != ReasonNoFile || len(cl.Windows) != 0 || cl.Provider != "claude" {
		t.Fatalf("claude = %+v", cl)
	}
	for _, p := range []string{"opencode", "agy"} {
		q := Unexposed(p)
		if q.Known || q.Reason != NotExposed || len(q.Windows) != 0 {
			t.Fatalf("%s must be unknown, not exposed: %+v", q.Provider, q)
		}
		if s := q.String(); s != q.Provider+" unknown (not exposed)" {
			t.Fatalf("String = %q", s)
		}
	}
	if q := ReadCodex(now, filepath.Join(dir, "missing")); q.Known || q.Reason != ReasonNoFile {
		t.Fatalf("missing codex dir = %+v", q)
	}
}

func TestQuotaBinding(t *testing.T) {
	win := func(label string, left float64, resetsAfter time.Duration) Window {
		return Window{Label: label, LeftPercent: left, ResetsAt: now.Add(resetsAfter)}
	}
	// Highest used (lowest left) wins.
	q := Quota{Provider: "claude", Known: true, Windows: []Window{win("5h", 88, time.Hour), win("wk", 58, 96*time.Hour)}}
	if w, ok := q.Binding(); !ok || w.Label != "wk" {
		t.Fatalf("highest used = %+v (ok %v)", w, ok)
	}
	// Equal used: the one that resets first wins.
	q = Quota{Provider: "claude", Known: true, Windows: []Window{win("5h", 60, 10*time.Hour), win("wk", 60, 2*time.Hour)}}
	if w, ok := q.Binding(); !ok || w.Label != "wk" {
		t.Fatalf("tie resets first = %+v (ok %v)", w, ok)
	}
	// An unknown window is skipped; only the known one can bind.
	q = Quota{Provider: "claude", Known: true, Windows: []Window{{Label: "5h"}, win("wk", 58, time.Hour)}}
	if w, ok := q.Binding(); !ok || w.Label != "wk" {
		t.Fatalf("skip unknown = %+v (ok %v)", w, ok)
	}
	// Not known, or no known window, binds nothing.
	if _, ok := (Quota{Provider: "claude", Reason: ReasonNoFile}).Binding(); ok {
		t.Fatal("unknown quota must not bind")
	}
	if _, ok := (Quota{Provider: "claude", Known: true, Windows: []Window{{Label: "5h"}}}).Binding(); ok {
		t.Fatal("no known window must not bind")
	}
}
