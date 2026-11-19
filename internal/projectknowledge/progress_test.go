package projectknowledge

import (
	"fmt"
	"sync"
	"testing"
)

func TestRefreshReportsScanAndIndexProgress(t *testing.T) {
	root, index := fixture(t)
	for i := 0; i < 30; i++ {
		write(t, root, fmt.Sprintf("pkg/f%02d.go", i), fmt.Sprintf("package pkg\nfunc F%02d() int { return %d }\n", i, i))
	}
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "add files for progress")

	var mu sync.Mutex
	var events []Progress
	ctx := WithProgress(t.Context(), func(p Progress) {
		mu.Lock()
		events = append(events, p)
		mu.Unlock()
	})
	if err := index.Ensure(ctx); err != nil {
		t.Fatal(err)
	}

	seen := map[string][]Progress{}
	for _, ev := range events {
		seen[ev.Phase] = append(seen[ev.Phase], ev)
	}
	for _, phase := range []string{"scan", "index"} {
		list := seen[phase]
		if len(list) < 2 {
			t.Fatalf("phase %q reported %d events (%+v), want >= 2", phase, len(list), list)
		}
		prev := -1
		for _, ev := range list {
			if ev.Total < 30 {
				t.Fatalf("phase %q total = %d, want >= 30", phase, ev.Total)
			}
			if ev.Done < prev || ev.Done > ev.Total {
				t.Fatalf("phase %q non-monotonic progress: prev=%d cur=%+v", phase, prev, ev)
			}
			prev = ev.Done
		}
		last := list[len(list)-1]
		if last.Done != last.Total {
			t.Fatalf("phase %q final progress = %d/%d, want complete", phase, last.Done, last.Total)
		}
	}
}
