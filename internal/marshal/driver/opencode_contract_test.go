package driver

import (
	"slices"
	"testing"

	"github.com/Zen1th53/marshal/internal/opencodecli"
)

func TestOpenCodeWorkerArgvFollowsInstalledCLI(t *testing.T) {
	saved := openCodeRunContract
	defer func() { openCodeRunContract = saved }()
	req := Request{Worktree: "/wt", Brief: "brief", Model: "m/x"}

	openCodeRunContract = func(string) (opencodecli.Contract, bool) {
		return opencodecli.Contract{Version: "2.1.4", Run: true, FormatJSON: true, Model: true, Standalone: true}, true
	}
	if got := OpenCode("").Args(req); !slices.Equal(got, []string{"run", "--standalone", "--format", "json", "-m", "m/x", "brief"}) {
		t.Fatalf("2.x worker argv (2.1.4 rejects --dir): %v", got)
	}

	openCodeRunContract = func(string) (opencodecli.Contract, bool) {
		return opencodecli.Contract{Version: "1.18.16", Run: true, FormatJSON: true, Model: true, Dir: true}, true
	}
	want := []string{"run", "--format", "json", "--dir", "/wt", "-m", "m/x", "brief"}
	if got := OpenCode("").Args(req); !slices.Equal(got, want) {
		t.Fatalf("1.x worker argv changed: %v", got)
	}
	openCodeRunContract = func(string) (opencodecli.Contract, bool) { return opencodecli.Contract{}, false }
	if got := OpenCode("").Args(req); !slices.Equal(got, want) {
		t.Fatalf("unobservable CLI argv changed: %v", got)
	}
}
