package usage

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// now is between the fixtures' last report and their resets: the 5h
// window resets at 19:08:49 UTC and the weekly one on 14 Nov.
var now = time.Date(2026, 11, 8, 18, 0, 0, 0, time.UTC)

// place writes data as dir/YYYY/MM/DD/name with modification time mod.
func place(t *testing.T, dir, day, name string, data []byte, mod time.Time) string {
	t.Helper()
	folder := filepath.Join(dir, filepath.FromSlash(day))
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(folder, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
	return path
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func codexFrom(t *testing.T, name string, mod time.Time) Quota {
	t.Helper()
	dir := t.TempDir()
	place(t, dir, "2026/11/08", "rollout-2026-11-08T17-00-00-s1.jsonl", fixture(t, name), mod)
	return readCodex(now, dir)
}

func TestCodexFresh(t *testing.T) {
	q := codexFrom(t, "fresh.jsonl", now.Add(-time.Minute))
	if !q.Known || q.Reason != "" || len(q.Windows) != 2 {
		t.Fatalf("fresh = %+v", q)
	}
	// The last complete report wins: not the earlier 5/40, and not the
	// unterminated 99 line still being written.
	want := []Window{
		{Label: "5h", LeftPercent: 82, ResetsAt: time.Unix(1794164929, 0)},
		{Label: "wk", LeftPercent: 49, ResetsAt: time.Unix(1794665063, 0)},
	}
	for i, w := range want {
		got := q.Windows[i]
		if got.Label != w.Label || got.LeftPercent != w.LeftPercent || !got.ResetsAt.Equal(w.ResetsAt) || !got.Known() {
			t.Fatalf("window %d = %+v, want %+v", i, got, w)
		}
	}
	if s := q.String(); s != "codex 5h 82% wk 49% left" {
		t.Fatalf("String = %q", s)
	}
}

func TestCodexUnknown(t *testing.T) {
	for _, tc := range []struct {
		name, file string
		mod        time.Time
		reason     string
	}{
		{"stale mtime", "fresh.jsonl", now.Add(-25 * time.Hour), ReasonStale},
		{"just stale", "fresh.jsonl", now.Add(-24 * time.Hour), ReasonStale},
		{"reset passed", "reset-passed.jsonl", now.Add(-time.Minute), ReasonReset},
		{"out of range", "out-of-range.jsonl", now.Add(-time.Minute), ReasonOutOfRange},
		{"missing field", "missing-field.jsonl", now.Add(-time.Minute), ReasonMissing},
		{"malformed", "malformed.jsonl", now.Add(-time.Minute), ReasonMalformed},
		{"trailing data", "trailing.jsonl", now.Add(-time.Minute), ReasonMalformed},
		{"null limits", "null-limits.jsonl", now.Add(-time.Minute), ReasonNotReported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := codexFrom(t, tc.file, tc.mod)
			if q.Known || q.Reason != tc.reason {
				t.Fatalf("got %+v, want unknown (%s)", q, tc.reason)
			}
			for _, w := range q.Windows {
				if w.Known() || w.LeftPercent != 0 {
					t.Fatalf("unknown provider has a percent: %+v", w)
				}
			}
		})
	}
}

func TestCodexPartialWindow(t *testing.T) {
	q := codexFrom(t, "reset-partial.jsonl", now.Add(-time.Minute))
	if !q.Known || len(q.Windows) != 2 {
		t.Fatalf("partial = %+v", q)
	}
	if q.Windows[0].Label != "5h" || q.Windows[0].Known() || q.Windows[0].LeftPercent != 0 {
		t.Fatalf("reset 5h window must be unknown: %+v", q.Windows[0])
	}
	if !q.Windows[1].Known() || q.Windows[1].LeftPercent != 49 {
		t.Fatalf("weekly = %+v", q.Windows[1])
	}
}

func TestCodexNoFile(t *testing.T) {
	for name, dir := range map[string]string{"empty dir": t.TempDir(), "missing dir": filepath.Join(t.TempDir(), "nope"), "no dir": ""} {
		if q := readCodex(now, dir); q.Known || q.Reason != ReasonNoFile {
			t.Fatalf("%s: %+v", name, q)
		}
	}
	// Files that are not rollouts, or not in a dated folder, do not count.
	dir := t.TempDir()
	place(t, dir, "2026/11/08", "history.jsonl", fixture(t, "fresh.jsonl"), now)
	place(t, dir, "latest/11/08", "rollout-x.jsonl", fixture(t, "fresh.jsonl"), now)
	if q := readCodex(now, dir); q.Known || q.Reason != ReasonNoFile {
		t.Fatalf("non-rollouts: %+v", q)
	}
	// A rollout with no rate_limits yet is not reported.
	place(t, dir, "2026/11/08", "rollout-a.jsonl", []byte("{\"type\":\"session_meta\"}\n"), now)
	if q := readCodex(now, dir); q.Known || q.Reason != ReasonNotReported {
		t.Fatalf("no report: %+v", q)
	}
}

func TestCodexNewestRollout(t *testing.T) {
	dir := t.TempDir()
	fresh, stale := fixture(t, "fresh.jsonl"), fixture(t, "reset-passed.jsonl")
	// An older day, and an older file in the newest day, lose to the
	// newest file of the newest day.
	place(t, dir, "2026/10/31", "rollout-old.jsonl", stale, now.Add(-time.Hour))
	place(t, dir, "2026/11/08", "rollout-b.jsonl", stale, now.Add(-30*time.Minute))
	place(t, dir, "2026/11/08", "rollout-c.jsonl", fresh, now.Add(-time.Minute))
	if q := readCodex(now, dir); !q.Known {
		t.Fatalf("newest = %+v", q)
	}
	// A session begun yesterday and still writing beats today's older one.
	dir = t.TempDir()
	place(t, dir, "2026/11/08", "rollout-today.jsonl", stale, now.Add(-30*time.Minute))
	place(t, dir, "2026/11/07", "rollout-yesterday.jsonl", fresh, now.Add(-time.Minute))
	if q := readCodex(now, dir); !q.Known {
		t.Fatalf("still-running session = %+v", q)
	}
	// Folders are ordered by date, not by modification time.
	dir = t.TempDir()
	place(t, dir, "2025/12/31", "rollout-y.jsonl", fresh, now.Add(-time.Minute))
	place(t, dir, "2026/01/01", "rollout-a.jsonl", stale, now.Add(-time.Minute))
	place(t, dir, "2026/01/02", "rollout-b.jsonl", stale, now.Add(-2*time.Minute))
	if q := readCodex(now, dir); q.Known || q.Reason != ReasonReset {
		t.Fatalf("dated order = %+v", q)
	}
}

func TestCodexCutLine(t *testing.T) {
	report := func(used string) string {
		return `{"type":"event_msg","payload":{"type":"token_count","rate_limits":{"primary":{"used_percent":` + used + `,"window_minutes":300,"resets_at":1794164929}}}}` + "\n"
	}
	pad := strings.Repeat("x", tailBytes)
	// The huge line mentions rate_limits and is valid JSON, but the tail
	// window cuts it, so the report after it is the one read.
	var b bytes.Buffer
	b.WriteString(report("1.0"))
	b.WriteString(`{"rate_limits":{"primary":{"used_percent":2.0,"window_minutes":300,"resets_at":1794164929}},"pad":"` + pad + `"}` + "\n")
	b.WriteString(report("18.0"))
	dir := t.TempDir()
	path := place(t, dir, "2026/11/08", "rollout-a.jsonl", b.Bytes(), now)
	q := readCodex(now, dir)
	if !q.Known || q.Windows[0].LeftPercent != 82 {
		t.Fatalf("after cut line = %+v", q)
	}
	// Alone, the cut line is skipped and nothing is reported; it is never
	// decoded from its remains.
	cut := `{"pad":"` + pad + `","rate_limits":{"primary":{"used_percent":2.0,"window_minutes":300,"resets_at":1794164929}}}` + "\n"
	if err := os.WriteFile(path, []byte(report("1.0")+cut), 0o644); err != nil {
		t.Fatal(err)
	}
	if q := readCodex(now, dir); q.Known || q.Reason != ReasonNotReported {
		t.Fatalf("cut line alone = %+v", q)
	}
	// A file within the tail window keeps its first line.
	if err := os.WriteFile(path, []byte(report("18.0")), 0o644); err != nil {
		t.Fatal(err)
	}
	if q := readCodex(now, dir); !q.Known {
		t.Fatalf("short file = %+v", q)
	}
}

func TestCodexStaleMark(t *testing.T) {
	stale := codexFrom(t, "fresh.jsonl", now.Add(-45*time.Minute))
	if !stale.Known || !stale.Stale || !stale.AsOf.Equal(now.Add(-45*time.Minute)) {
		t.Fatalf("45m old must be Known and Stale: %+v", stale)
	}
	fresh := codexFrom(t, "fresh.jsonl", now.Add(-time.Minute))
	if !fresh.Known || fresh.Stale || !fresh.AsOf.Equal(now.Add(-time.Minute)) {
		t.Fatalf("1m old must be Known, not Stale: %+v", fresh)
	}
}

func TestCodexWindowLabels(t *testing.T) {
	for minutes, want := range map[int64]string{300: "5h", 10080: "wk", 60: "60m", 1440: "1440m"} {
		if got := windowLabel(minutes); got != want {
			t.Errorf("windowLabel(%d) = %q, want %q", minutes, got, want)
		}
	}
	q := codexFrom(t, "other-window.jsonl", now.Add(-time.Minute))
	if !q.Known || len(q.Windows) != 1 || q.Windows[0].Label != "60m" {
		t.Fatalf("other window = %+v", q)
	}
}
