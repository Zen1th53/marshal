//go:build !governed

package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/execution"
)

func agySigninExpiryNotices(t *testing.T, r *Runtime) int {
	t.Helper()
	history, err := r.store.ListEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, event := range history {
		if event.Type == agySigninExpiryEvent {
			if event.Data["message"] != agySigninExpiryNotice {
				t.Fatalf("notice message = %v", event.Data["message"])
			}
			n++
		}
	}
	return n
}

func TestAgySigninExpiryCooldownAndSingleNotice(t *testing.T) {
	ResetProviderCooldowns()
	t.Cleanup(ResetProviderCooldowns)
	r := openNetpolRuntime(t)
	var delivered []EgressAlert
	r.SetEgressAlertSink(func(a EgressAlert) error { delivered = append(delivered, a); return nil })
	task := execution.TaskExecution{TaskID: "T1", RunID: "RUN-AGY-SIGNIN", CanonicalTaskID: "TASK-AGY"}
	started := time.Now()
	expired := adapter.Result{ExitCode: 1, Stderr: []byte("Error: sign-in token expired, please sign in again"), StartedAt: started, EndedAt: started.Add(time.Second)}

	ok, err := r.noteAgySigninExpiry(context.Background(), task, errors.New("exit status 1"), expired)
	if err != nil || !ok {
		t.Fatalf("noteAgySigninExpiry = %v, %v", ok, err)
	}
	until, cooling := getProviderCooldownUntil("agy")
	if !cooling || !isProviderUnavailable("agy", time.Now()) {
		t.Fatal("agy is not cooling down after its sign-in expired")
	}
	if until.After(time.Now().Add(agySigninExpiryCooldown)) {
		t.Fatalf("cooldown %v exceeds the agy sign-in bound", until)
	}
	if _, limited := r.providerLimited("antigravity"); !limited {
		t.Fatal("agy is still offered for retry during its cooldown")
	}
	if n := agySigninExpiryNotices(t, r); n != 1 {
		t.Fatalf("notices = %d, want 1", n)
	}
	if len(delivered) != 1 || delivered[0].Message != agySigninExpiryNotice || delivered[0].Worker != "agy" || delivered[0].RunID != task.RunID {
		t.Fatalf("delivered = %+v", delivered)
	}

	// A second failure in the same episode records no second notice.
	ok, err = r.noteAgySigninExpiry(context.Background(), task, nil, adapter.Result{ExitCode: 1, StartedAt: started, EndedAt: started.Add(100 * time.Millisecond)})
	if err != nil || !ok {
		t.Fatalf("second noteAgySigninExpiry = %v, %v", ok, err)
	}
	if n := agySigninExpiryNotices(t, r); n != 1 || len(delivered) != 1 {
		t.Fatalf("notices = %d delivered = %d after a repeated failure, want 1", n, len(delivered))
	}
}

func TestAgySigninExpiryOrdinaryFailureNoCooldown(t *testing.T) {
	ResetProviderCooldowns()
	t.Cleanup(ResetProviderCooldowns)
	r := openNetpolRuntime(t)
	var delivered []EgressAlert
	r.SetEgressAlertSink(func(a EgressAlert) error { delivered = append(delivered, a); return nil })
	started := time.Now()
	ok, err := r.noteAgySigninExpiry(context.Background(), execution.TaskExecution{TaskID: "T1", RunID: "RUN-AGY-ORD"}, nil,
		adapter.Result{ExitCode: 1, Stderr: []byte("main.go:3: undefined: foo"), StartedAt: started, EndedAt: started.Add(2 * time.Minute)})
	if err != nil || ok {
		t.Fatalf("noteAgySigninExpiry = %v, %v", ok, err)
	}
	if isProviderUnavailable("agy", time.Now()) {
		t.Fatal("ordinary agy failure put agy into cooldown")
	}
	if n := agySigninExpiryNotices(t, r); n != 0 || len(delivered) != 0 {
		t.Fatalf("ordinary failure notified the owner: %d %d", n, len(delivered))
	}
}
