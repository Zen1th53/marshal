package app

import (
	"context"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/netpolicy"
)

func TestRecoveredEgressNotificationsRequireLivePendingRun(t *testing.T) {
	r := openNetpolRuntime(t)
	scope := &runEgress{id: "RUN-recovered", worker: "worker", pending: map[string]bool{}}
	r.egressRuns = map[string]*runEgress{scope.id: scope}
	if err := r.recordEgressAttempt(context.Background(), scope, "example.test", 443, netpolicy.Decision{Reason: netpolicy.ReasonDenied}); err != nil {
		t.Fatal(err)
	}
	check := func(wanted string) {
		t.Helper()
		alerts, err := r.EgressNotifications(context.Background())
		if err != nil || len(alerts) != 1 || alerts[0].State != wanted {
			t.Fatalf("alerts=%+v err=%v wanted=%s", alerts, err, wanted)
		}
	}
	check("waiting")
	scope.mu.Lock()
	delete(scope.pending, "example.test:443")
	scope.mu.Unlock()
	check("expired")
	scope.mu.Lock()
	scope.pending["example.test:443"] = true
	scope.mu.Unlock()
	r.egressMu.Lock()
	delete(r.egressRuns, scope.id)
	r.egressMu.Unlock()
	check("expired")
	if r.EgressRequestPending(scope.id, "example.test:443") {
		t.Fatal("ended run remains pending")
	}
}

func TestDeterministicOutcomeStoredAsSystemRecord(t *testing.T) {
	st, svc := openTestMemoryService(t)
	rec, err := svc.CaptureOutcome(context.Background(), OutcomeCaptureRequest{ProjectID: "PROJECT-local", TaskID: "TASK-system", RunID: "RUN-system", Status: "success"})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := st.Store().GetMemoryV2(context.Background(), rec.ProjectID, rec.ID)
	if err != nil || stored.Authority != model.AuthorityPolicy || stored.ExtMeta["record_class"] != "system_record" || !strings.HasPrefix(stored.Title, "System record · ") {
		t.Fatalf("system evidence mislabeled: %+v %v", stored, err)
	}
}
