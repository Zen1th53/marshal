//go:build governed

package tui

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/cloud"
)

func TestGovernedUltraStartRequiresLease(t *testing.T) {
	st, _ := preferenceStore(t)
	w := NewWorkspace(st, "project", "session")
	defer w.Close()
	if _, err := w.ExecuteCommand(t.Context(), "/ultra start"); err != nil {
		t.Fatal(err)
	}
	if w.ULTRAExecutionEnabled() {
		t.Fatal("start without lease enabled execution")
	}
	rec, err := st.GetUltraExecutionPreference(t.Context(), "project")
	if err != nil || rec != nil {
		t.Fatalf("refused start wrote preference: %+v %v", rec, err)
	}
}

func TestUltraStatusSeparatesRequestFromLease(t *testing.T) {
	for _, tc := range []struct{ state, want string }{
		{"pending", "PENDING"}, {"denied", "DENIED"}, {"active", "APPROVED"}, {"sending", "UNKNOWN"},
	} {
		t.Run(tc.state, func(t *testing.T) {
			authority := &trackedRequestAuthority{row: cloud.RequestObservation{LocalAttemptID: "attempt", State: tc.state}}
			w := NewWorkspace(nil, "project", "session")
			defer w.Close()
			w.AttachControlSource(&ControlSource{Authority: authority})
			out, err := w.ExecuteCommand(t.Context(), "/ultra status")
			if err != nil || !strings.Contains(out, "Entitlement request: "+tc.want) || !strings.Contains(out, "INACTIVE") {
				t.Fatalf("status: %q %v", out, err)
			}
			if w.ULTRAExecutionEnabled() || authority.submitCalls != 0 {
				t.Fatal("status changed execution or submitted a request")
			}
		})
	}
}
