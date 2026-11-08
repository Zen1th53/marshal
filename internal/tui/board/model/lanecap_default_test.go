//go:build governed

package model

import (
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
)

func TestLaneCapIsTheRunsConcurrency(t *testing.T) {
	r := run(task("T-1", marshal.Dispatched))
	if got, want := Project(Snapshot{RunID: "R1", Run: r}, nil).Run.LaneCap, marshal.DefaultSettings().UltraConcurrency; got != want {
		t.Fatalf("unset cap = %d, want the default concurrency %d", got, want)
	}
	r.Settings.UltraConcurrency = 2
	if got := Project(Snapshot{RunID: "R1", Run: r}, nil).Run.LaneCap; got != 2 {
		t.Fatalf("cap = %d, want the run's concurrency 2", got)
	}
}
