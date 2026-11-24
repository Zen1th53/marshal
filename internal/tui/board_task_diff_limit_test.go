package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

func diffFile(name string, size int) string {
	head := "diff --git a/" + name + " b/" + name + "\n@@ -1 +1 @@\n+"
	return head + strings.Repeat("x", size-len(head)-1) + "\n"
}

// A task diff of exactly a Board page is shown whole; one byte more pages
// by whole files with a cursor bound to the task and commit, a cursor for
// another task or commit is refused, and a file larger than a page names
// the commit to inspect it in.
func TestBoardDiffLimitAtCap(t *testing.T) {
	half := taskDiffLimit / 2
	exact := diffFile("a.go", half) + diffFile("b.go", half)
	page, limit, err := pageTaskDiff("T1", "c0ffee", exact, "")
	if err != nil || limit != nil || page != exact || len(exact) != taskDiffLimit {
		t.Fatalf("at bound: %v %+v", err, limit)
	}
	over := diffFile("a.go", half) + diffFile("b.go", half+1)
	page, limit, err = pageTaskDiff("T1", "c0ffee", over, "")
	if err != nil || limit == nil || page != diffFile("a.go", half) || limit.Cursor != "T1@c0ffee#1" || limit.EvidenceRef != "c0ffee" || limit.ActualBytes != taskDiffLimit+1 || limit.KeptBytes != int64(half) {
		t.Fatalf("one over: %v %+v", err, limit)
	}
	rest, restLimit, err := pageTaskDiff("T1", "c0ffee", over, limit.Cursor)
	if err != nil || rest != diffFile("b.go", half+1) || restLimit == nil || restLimit.Cursor != "" {
		t.Fatalf("next page: %v %+v", err, restLimit)
	}
	for _, foreign := range []struct{ task, commit string }{{"T2", "c0ffee"}, {"T1", "beef"}} {
		if _, _, err := pageTaskDiff(foreign.task, foreign.commit, over, limit.Cursor); !errors.Is(err, model.ErrPolicyDenied) {
			t.Fatalf("foreign cursor %+v: %v", foreign, err)
		}
	}
	huge := diffFile("big.bin", taskDiffLimit+1) + diffFile("c.go", 64)
	page, limit, err = pageTaskDiff("T1", "c0ffee", huge, "")
	if err != nil || page != "" || limit == nil || !strings.Contains(limit.Action, "a/big.bin") || !strings.Contains(limit.Action, "c0ffee") || limit.Cursor != "T1@c0ffee#1" {
		t.Fatalf("huge file: %v %+v", err, limit)
	}
}
