//go:build !governed

package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectcontext"
)

// The largest hidden addition that fits a launch is admitted whole; one
// byte more is refused with ErrBudget, a typed limit naming the part and
// sizes, and a field-specific action, and the launch arguments are kept.
func TestNativePlanningLimitAtCap(t *testing.T) {
	root := t.TempDir()
	base := []string{"-c", "developer_instructions=MARSHAL PROTOCOL"}
	try := func(n int) error {
		args := append([]string(nil), base...)
		_, err := appendNativePlanningHiddenExact("codex", root, args, nil, strings.Repeat("q", n))
		if args[1] != base[1] {
			t.Fatal("refusal changed the launch arguments")
		}
		return err
	}
	low, high := 0, nativePlanningCombinedMaximumBytes
	for low < high {
		mid := (low + high + 1) / 2
		if try(mid) == nil {
			low = mid
		} else {
			high = mid - 1
		}
	}
	if low < nativePlanningCombinedMaximumBytes/2 {
		t.Fatalf("largest admitted addition is only %d bytes", low)
	}
	if err := try(low); err != nil {
		t.Fatalf("at bound: %v", err)
	}
	err := try(low + 1)
	limit, ok := model.LimitOf(err)
	if !errors.Is(err, projectcontext.ErrBudget) || !ok || limit.Kind != "native_planning_launch" || limit.CapBytes != nativePlanningCombinedMaximumBytes || limit.ActualBytes != nativePlanningCombinedMaximumBytes+1 || !strings.Contains(limit.Action, "marshal_project_contract_context") || !strings.Contains(err.Error(), limit.Subject) {
		t.Fatalf("one over: %+v %v", limit, err)
	}
}
