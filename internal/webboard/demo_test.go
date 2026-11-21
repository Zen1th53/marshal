package webboard

import (
	"os"
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
// updates can be seen; Ctrl+C stops it.
func TestDemo(t *testing.T) {
	name := os.Getenv("MARSHAL_WEBBOARD_DEMO")
	if name == "" {
		t.Skip("set MARSHAL_WEBBOARD_DEMO=running|ready|planning|decision|paused to serve the demo")
	}
	base, ok := fixture(name)
	if !ok {
		t.Fatalf("no fixture %q", name)
	}
	addr := os.Getenv("MARSHAL_WEBBOARD_DEMO_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8765"
	}
	began := time.Now()
	src := func() vm.Board {
		b := base
		now := time.Now()
		b.Now, b.Top.Clock = now, now
		run := now.Sub(began).Truncate(time.Second)
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
	s, err := start(addr, "demo", src)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	t.Logf("Web Board demo (%s): %s", name, s.URL)
	select {}
}
