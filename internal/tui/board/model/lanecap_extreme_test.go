//go:build !governed

package model

import (
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
)

func TestLaneCapIsTheRunsGlobalCap(t *testing.T) {
	r := run(task("T-1", marshal.Dispatched))
	if got := Project(Snapshot{RunID: "R1", Run: r}, nil).Run.LaneCap; got != defaultGlobalCap {
		t.Fatalf("unset cap = %d, want the dispatcher's default %d", got, defaultGlobalCap)
	}
	r.Settings.GlobalCap = 5
	if got := Project(Snapshot{RunID: "R1", Run: r}, nil).Run.LaneCap; got != 5 {
		t.Fatalf("cap = %d, want the run's global cap 5", got)
	}
}
