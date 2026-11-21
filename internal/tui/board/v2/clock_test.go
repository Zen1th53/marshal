package v2

import (
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/tui/board/vm"
)

// localZone sets time.Local to zone for one test and restores it.
func localZone(t *testing.T, zone *time.Location) {
	t.Helper()
	prev := time.Local
	time.Local = zone
	t.Cleanup(func() { time.Local = prev })
}

// Every time on the Board is the local wall clock. Run events come back from
// the store in UTC and notes are stamped in local time; on a workstation in
// UTC+5 the Activity pane showed "06:56 review" next to "11:56 note" for
// events a minute apart. A folded row shows its latest time, the one the rows
// are ordered by.
func TestBoardClocksAreLocal(t *testing.T) {
	plus5 := time.FixedZone("UTC+5", 5*3600)
	localZone(t, plus5)
	utc := func(h, m int) time.Time { return time.Date(2026, 11, 21, h, m, 0, 0, time.UTC) }
	local := func(h, m int) time.Time { return time.Date(2026, 11, 21, h, m, 0, 0, plus5) }

	b := fixtureRunning()
	b.Top.Clock = utc(6, 57)
	b.Activity = []vm.Event{
		{At: local(11, 56), Kind: "note", Text: "Marshal decision requested", Tone: vm.ToneNone},
		{At: utc(6, 56), Last: utc(6, 56), Kind: "review", Text: "rv03 accepted", Tone: vm.ToneVio},
		{At: local(11, 40), Last: local(11, 55), Kind: "note", Text: "Task rv03 completed", Count: 3},
		{At: utc(6, 53), Kind: "plan", Text: "rv03 started", Tone: vm.ToneInfo},
		{At: utc(6, 53), Kind: "error", Text: "rv02 stuck", Tone: vm.ToneBad},
	}
	lines := Render(b, 132, 40, Theme{Color: ColorNone, Unicode: true})
	screen := strip(strings.Join(lines, "\n"))
	want := []string{"11:56  note", "11:56  review", "11:55  note", "11:53  plan", "11:53  error"}
	last := -1
	for _, w := range want {
		i := strings.Index(screen, w)
		if i < 0 {
			t.Fatalf("no %q in Activity:\n%s", w, screen)
		}
		if i < last {
			t.Fatalf("%q is out of order:\n%s", w, screen)
		}
		last = i
	}
	for _, bad := range []string{"06:56", "06:53", "06:57", "11:40"} {
		if strings.Contains(screen, bad) {
			t.Fatalf("Board shows %s, a UTC or first-fold time:\n%s", bad, screen)
		}
	}
	if !strings.Contains(screen, "11:57") {
		t.Fatalf("top bar clock is not local 11:57:\n%s", screen)
	}
}

func TestClockConvertsToLocal(t *testing.T) {
	localZone(t, time.FixedZone("UTC+5", 5*3600))
	at := time.Date(2026, 11, 21, 6, 56, 0, 0, time.UTC)
	if got := clock(at); got != "11:56" {
		t.Fatalf("clock(%v) = %q, want 11:56", at, got)
	}
	if got := clock(at.In(time.FixedZone("UTC-3", -3*3600))); got != "11:56" {
		t.Fatalf("clock from UTC-3 = %q, want 11:56", got)
	}
	if got := clock(time.Time{}); got != dash {
		t.Fatalf("zero clock = %q", got)
	}
}
