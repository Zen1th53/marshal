package usage

import (
	"path/filepath"
	"testing"
	"time"
)

func TestReadOrderAndNotExposed(t *testing.T) {
	dir := t.TempDir()
	place(t, dir, "2026/11/08", "rollout-a.jsonl", fixture(t, "fresh.jsonl"), now.Add(-time.Minute))
	qs := Read(now, Sources{CodexDir: dir})
	if len(qs) != 4 {
		t.Fatalf("Read = %+v", qs)
	}
	for i, p := range []string{"codex", "claude", "opencode", "agy"} {
		if qs[i].Provider != p {
			t.Fatalf("Read[%d] = %q, want %q", i, qs[i].Provider, p)
		}
	}
	if !qs[0].Known {
		t.Fatalf("codex = %+v", qs[0])
	}
	// With no ClaudeFile, claude is unknown with ReasonNoFile, not a number.
	if cl := qs[1]; cl.Known || cl.Reason != ReasonNoFile || len(cl.Windows) != 0 {
		t.Fatalf("claude = %+v", cl)
	}
	for _, q := range qs[2:] {
		if q.Known || q.Reason != NotExposed || len(q.Windows) != 0 {
			t.Fatalf("%s must be unknown, not exposed: %+v", q.Provider, q)
		}
		if s := q.String(); s != q.Provider+" unknown (not exposed)" {
			t.Fatalf("String = %q", s)
		}
	}
	if q := Read(now, Sources{CodexDir: filepath.Join(dir, "missing")})[0]; q.Known || q.Reason != ReasonNoFile {
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
