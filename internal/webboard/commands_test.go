package webboard

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Zen1th53/marshal/internal/store"
)

func TestCmdTokenCannotCrossTargetOrOp(t *testing.T) {
	h := newSettingsHarness(t)
	caps := []SettingCapability{
		{Op: "task/retry", Target: Target{Kind: "task", ID: "first"}, Revision: 1, RunID: "run", AttemptID: "attempt"},
		{Op: "task/skip", Target: Target{Kind: "task", ID: "first"}, Revision: 1, RunID: "run", AttemptID: "attempt"},
		{Op: "task/retry", Target: Target{Kind: "task", ID: "second"}, Revision: 1, RunID: "run", AttemptID: "attempt"},
	}
	h.s.settings.Load().Source = func() SettingsState {
		return SettingsState{ProjectID: "project", Fence: "session", Capabilities: append([]SettingCapability(nil), caps...)}
	}
	calls := 0
	h.s.sink = func(_ context.Context, a Action) Result {
		return a.Commit(func() Result {
			calls++
			if err := h.st.StartWebRequest(context.Background(), *a.Command, a.Command.AttemptID); err != nil {
				return Result{Status: "refused"}
			}
			_ = h.st.FinishWebRequest(context.Background(), *a.Command, store.WebReceipt{RequestID: a.Command.RequestID, Status: "applied", Code: "applied", Effects: []string{"run-command"}})
			return Result{Status: "done"}
		})
	}
	cap := h.s.settingsFrame().Capabilities[0]
	req := actRequest{Action: "cmd", Token: cap.Token, Command: Command{RequestID: "00112233445566778899aabbccddeeff", ProjectID: "project", Target: cap.Target, Op: cap.Op, Revision: 1, RunID: "run", AttemptID: "attempt"}}
	for _, change := range []func(*actRequest){func(r *actRequest) { r.Target.ID = "second" }, func(r *actRequest) { r.Op = "task/skip" }, func(r *actRequest) { r.AttemptID = "replacement" }, func(r *actRequest) { r.RunID = "other" }} {
		bad := req
		bad.Preview = true
		change(&bad)
		if response := h.send(bad, "http://"+h.s.addr); response.Code != 409 {
			t.Fatalf("crossed binding: %d %s", response.Code, response.Body.String())
		}
	}
	req.Preview = true
	response := h.send(req, "http://"+h.s.addr)
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	var result struct {
		Confirmation Confirmation `json:"confirmation"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	req.Preview = false
	req.Confirmation = &result.Confirmation
	req.Token = h.s.settingsFrame().Capabilities[0].Token
	bad := req
	bad.Payload.Reason = "different payload"
	if response = h.send(bad, "http://"+h.s.addr); response.Code != 409 {
		t.Fatal("confirmation accepted a different payload")
	}
	for range 2 {
		if response = h.send(req, "http://"+h.s.addr); response.Code != 200 {
			t.Fatal(response.Body.String())
		}
	}
	if calls != 1 {
		t.Fatalf("replayed effect %d times", calls)
	}
}

func TestRunCommandTargetBindsSelectedRun(t *testing.T) {
	ok := Command{Op: "run/stop-workers", Target: Target{Kind: "run", ID: "run"}, RunID: "run", AttemptID: "workers"}
	if !validCommandTarget(ok) || !validWorkPayload(ok) {
		t.Fatal("bound run control refused")
	}
	for _, change := range []func(*Command){
		func(c *Command) { c.RunID = "other" },
		func(c *Command) { c.AttemptID = "" },
		func(c *Command) { c.Op = "task/retry" },
		func(c *Command) { c.Payload.Provider = "codex" },
	} {
		bad := ok
		change(&bad)
		if validCommandTarget(bad) && validWorkPayload(bad) {
			t.Fatalf("crossed run binding accepted: %+v", bad)
		}
	}
}

func TestRunControlTokenIsBoundAndSingleUse(t *testing.T) {
	h := newSettingsHarness(t)
	caps := []SettingCapability{
		{Op: "run/pause", Target: Target{Kind: "run", ID: "run"}, RunID: "run", AttemptID: "plan"},
		{Op: "run/stop-workers", Target: Target{Kind: "run", ID: "run"}, RunID: "run", AttemptID: "workers"},
	}
	h.s.settings.Load().Source = func() SettingsState {
		return SettingsState{ProjectID: "project", Fence: "session", Capabilities: append([]SettingCapability(nil), caps...)}
	}
	calls := 0
	h.s.sink = func(_ context.Context, a Action) Result {
		return a.Commit(func() Result {
			calls++
			if err := h.st.StartWebRequest(context.Background(), *a.Command, a.Command.AttemptID); err != nil {
				return Result{Status: "refused"}
			}
			_ = h.st.FinishWebRequest(context.Background(), *a.Command, store.WebReceipt{RequestID: a.Command.RequestID, Status: "applied", Code: "applied", Effects: []string{"run-command"}})
			return Result{Status: "done"}
		})
	}
	cap := h.s.settingsFrame().Capabilities[0]
	req := actRequest{Action: "cmd", Token: cap.Token, Command: Command{RequestID: "a0112233445566778899aabbccddeeff", ProjectID: "project", Target: cap.Target, Op: cap.Op, RunID: "run", AttemptID: "plan", Preview: true}}
	claims, _ := h.s.readClaims(cap.Token)
	claims.Expires = 1
	expired := req
	expired.Token = h.s.signClaims(claims)
	for _, bad := range []actRequest{expired, func() actRequest { r := req; r.Op = "run/stop-workers"; return r }(), func() actRequest { r := req; r.AttemptID = "changed-plan"; return r }()} {
		if response := h.send(bad, "http://"+h.s.addr); response.Code != 409 {
			t.Fatalf("unbound run control: %d %s", response.Code, response.Body.String())
		}
	}
	response := h.send(req, "http://"+h.s.addr)
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	var result struct {
		Confirmation Confirmation `json:"confirmation"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	req.Preview, req.Confirmation = false, &result.Confirmation
	req.Token = h.s.settingsFrame().Capabilities[0].Token
	for range 2 {
		if response = h.send(req, "http://"+h.s.addr); response.Code != 200 {
			t.Fatal(response.Body.String())
		}
	}
	if calls != 1 {
		t.Fatalf("run control ran %d times", calls)
	}
}
