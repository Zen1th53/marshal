package usage

import (
	"path/filepath"
	"testing"
	"time"
)

func TestReadOrderAndNotExposed(t *testing.T) {
	dir := t.TempDir()
	place(t, dir, "2026/11/08", "rollout-a.jsonl", fixture(t, "fresh.jsonl"), now.Add(-time.Minute))
	qs := Read(now, dir)
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
	for _, q := range qs[1:] {
		if q.Known || q.Reason != NotExposed || len(q.Windows) != 0 {
			t.Fatalf("%s must be unknown, not exposed: %+v", q.Provider, q)
		}
		if s := q.String(); s != q.Provider+" unknown (not exposed)" {
			t.Fatalf("String = %q", s)
		}
	}
	if q := Read(now, filepath.Join(dir, "missing"))[0]; q.Known || q.Reason != ReasonNoFile {
		t.Fatalf("missing codex dir = %+v", q)
	}
}
