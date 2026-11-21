package tui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
)

func TestArchitectureRequestBoxDeliversOnlyToAWaitingRequest(t *testing.T) {
	var box architectureRequestBox
	if box.deliver("r1", app.ArchitectureAnswer{Status: "answered"}) {
		t.Fatal("an answer to no waiting request must be refused")
	}
	ch := box.open("r1")
	if !box.deliver("r1", app.ArchitectureAnswer{Status: "answered", Reply: "ok"}) {
		t.Fatal("the waiting request did not get its answer")
	}
	select {
	case a := <-ch:
		if a.Reply != "ok" {
			t.Fatalf("answer = %+v", a)
		}
	case <-time.After(time.Second):
		t.Fatal("answer never arrived")
	}
	if box.deliver("r1", app.ArchitectureAnswer{Status: "answered"}) {
		t.Fatal("a request is answered once")
	}
	box.open("r2")
	box.close("r2")
	if box.deliver("r2", app.ArchitectureAnswer{Status: "answered"}) {
		t.Fatal("a withdrawn or timed-out request takes no answer")
	}
}

func TestArchitectureRequestMessageQuotesTheExactSelection(t *testing.T) {
	msg := architectureRequestMessage(app.ArchitectureRequest{
		ID: "req-7", Type: "change", Text: `Login must "lock" after 5 tries`, Revision: json.Number("3"),
		Source:      json.RawMessage(`{"id":"site","revision":2,"digest":"d2"}`),
		Anchor:      json.RawMessage(`{"kind":"component","id":"login"}`),
		Components:  json.RawMessage(`[{"id":"login","responsibility":"Sign in"}]`),
		Connections: json.RawMessage(`[]`),
	})
	for _, want := range []string{"## architecture request req-7", "Change request", "revision 3", `"Login must \"lock\" after 5 tries"`,
		`"components":[{"id":"login","responsibility":"Sign in"}]`, `"source_contract":{"id":"site","revision":2,"digest":"d2"}`,
		"marshal_architecture_request_answer", "marshal_project_contract_submit"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message lacks %q:\n%s", want, msg)
		}
	}
	if !strings.HasPrefix(msg, "\n\n## ") {
		t.Error("an inbox entry starts a new section")
	}
}

func TestForwardedRequestTimesOutWithAPlainReason(t *testing.T) {
	var box architectureRequestBox
	ch := box.open("r")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	select {
	case <-ch:
		t.Fatal("no answer was sent")
	case <-ctx.Done():
	}
	if !strings.Contains(errArchitectureRequestUnanswered.Error(), "did not answer") {
		t.Fatal("timeout reason must be plain")
	}
}

func TestArchitectureRequestMessageForDerivedCodeForbidsAChange(t *testing.T) {
	msg := architectureRequestMessage(app.ArchitectureRequest{
		ID: "req-8", Type: "change", Text: "Split this file", Revision: json.Number("3"),
		Source:      json.RawMessage(`{"id":"site","revision":2,"digest":"d2"}`),
		Anchor:      json.RawMessage(`{"kind":"component","id":"login"}`),
		Components:  json.RawMessage(`[{"id":"login"}]`),
		Connections: json.RawMessage(`[]`),
		Derived:     json.RawMessage(`{"part":"login","elements":[{"kind":"file","path":"login/form.go"}],"note":"derived from code, not reviewed"}`),
	})
	for _, want := range []string{"Question about code", "derived from code, not reviewed", `"path":"login/form.go"`, "Never answer proposed_change", "submit no project contract revision"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message lacks %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "For proposed_change first submit") {
		t.Error("a derived request must not invite a contract revision")
	}
}
