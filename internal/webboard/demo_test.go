package webboard

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/tui/board/vm"
)

// TestDemo serves a fixture Board so the page can be looked at without a
// live run. It runs only when asked:
//
//	MARSHAL_WEBBOARD_DEMO=running go test ./internal/webboard -run TestDemo -v -timeout 0
//
// then open http://127.0.0.1:8765/?t=demo (screens: running, ready,
// planning, decision, paused; MARSHAL_WEBBOARD_DEMO_ADDR changes the
// address). The lanes tick and a merge lands every 20 seconds, so the live
// updates can be seen; Ctrl+C stops it. Actions go to a fake sink: the
// card's first button clears the card (the run goes on), a message is
// "sent", and each writes an Activity line as the Board would.
func TestDemo(t *testing.T) {
	name := os.Getenv("MARSHAL_WEBBOARD_DEMO")
	if name == "" {
		t.Skip("set MARSHAL_WEBBOARD_DEMO=running|ready|planning|decision|paused to serve the demo")
	}
	if _, ok := fixture(name); !ok {
		t.Fatalf("no fixture %q", name)
	}
	addr := os.Getenv("MARSHAL_WEBBOARD_DEMO_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8765"
	}
	d := newDemo(name)
	s, err := start(addr, "demo", d.source, d.act)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	link, _ := s.Link()
	t.Logf("Web Board demo (%s): %s", name, link)
	select {}
}

// demo is a fixture Board that moves and takes actions.
type demo struct {
	mu    sync.Mutex
	began time.Time
	base  vm.Board
	notes []vm.Event
}

func newDemo(name string) *demo {
	b, _ := fixture(name)
	withScreenKeys(&b)
	return &demo{began: time.Now(), base: b}
}

// withScreenKeys gives a running Board the screen's own buttons, as the
// Board offers them: retry a stuck task and stop all workers.
func withScreenKeys(b *vm.Board) {
	if b.Decision == nil && b.Paused == nil && b.Top.Run != "" {
		b.Keys = kv("r", "retry T12", "ctrl+x", "stop all workers")
	}
}

func (d *demo) source() vm.Board {
	d.mu.Lock()
	b := d.base
	notes := append([]vm.Event(nil), d.notes...)
	d.mu.Unlock()
	b.Activity = append(notes, b.Activity...)
	now := time.Now()
	b.Now, b.Top.Clock = now, now
	run := now.Sub(d.began).Truncate(time.Second)
	lanes := append([]vm.Lane(nil), b.Lanes.Items...)
	for i := range lanes {
		lanes[i].Elapsed += run
		// Heartbeats arrive every few seconds; agy-4 stays silent.
		if !lanes[i].Stale {
			lanes[i].LastSignal = time.Duration(int(run/time.Second)%(5+i)) * time.Second
		} else {
			lanes[i].LastSignal += run
		}
	}
	b.Lanes.Items = lanes
	if b.Plan.Total > 0 {
		merged := int(run / (20 * time.Second))
		b.Plan.Done = min(b.Plan.Total, b.Plan.Done+merged)
		b.Plan.Throughput = append(append([]int(nil), b.Plan.Throughput...), merged%4+4)
		if len(b.Plan.Throughput) > 12 {
			b.Plan.Throughput = b.Plan.Throughput[len(b.Plan.Throughput)-12:]
		}
	}
	return b
}

// act is the demo's fake sink.
func (d *demo) act(_ context.Context, a Action) Result {
	d.mu.Lock()
	defer d.mu.Unlock()
	note := func(text string) {
		d.notes = append([]vm.Event{{At: time.Now(), Kind: "you", Text: text, Tone: vm.ToneInfo}}, d.notes...)
	}
	if a.Kind == "say" {
		note("From the web: a message to the Marshal (sent).")
		return Result{Status: "sent", Text: "Sent to the Marshal."}
	}
	var ks []vm.Key
	title := ""
	switch {
	case d.base.Decision != nil:
		ks, title = d.base.Decision.Actions, d.base.Decision.Title
	case d.base.Paused != nil:
		ks, title = d.base.Paused.Actions, d.base.Paused.Title
	default:
		for _, k := range d.base.Keys {
			if k.Key == a.Key {
				note("From the web: " + k.Label + ".")
			}
		}
		if a.Key == "r" {
			d.base.Keys = kv("ctrl+x", "stop all workers")
			return Result{Status: "done", Text: "T12 starts again in a fresh session. The run continues."}
		}
		return Result{Status: "done", Text: "Stopped all workers; the Marshal keeps running."}
	}
	label := a.Key
	for _, k := range ks {
		if k.Key == a.Key {
			label = k.Label
		}
	}
	note("From the web: " + label + " (" + title + ").")
	switch {
	case a.Key == "d":
		return Result{Status: "done", Text: "Raw error: 429 RESOURCE_EXHAUSTED: Gemini 5h quota for this project is used up."}
	case a.Key == "a" && d.base.Decision != nil:
		return Result{Status: "done", Text: "The architecture is open in your browser.", Link: "http://127.0.0.1:37667/?token=demo"}
	case len(ks) > 0 && a.Key == ks[0].Key, a.Key == "x":
		r := fixtureRunning()
		r.Activity = d.base.Activity
		if d.base.Paused != nil {
			r.Activity = fixtureRunning().Activity
		}
		withScreenKeys(&r)
		d.base = r
		if a.Key == "x" {
			return Result{Status: "done", Text: "Stopped. The run and its work are kept; /marshal resume continues it."}
		}
		return Result{Status: "done", Text: "Moving T06 and T09 to claude and resuming the run."}
	}
	return Result{Status: "done"}
}
