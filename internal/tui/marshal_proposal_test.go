package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/tmux"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/importer"
	"github.com/Zen1th53/marshal/internal/permission"
)

func TestMarshalProposalSettingPermission(t *testing.T) {
	for _, allow := range []bool{true, false} {
		w, rt := realControlWorkspace(t, "SESSION-proposal")
		tr := importer.SessionTranscript{SessionID: "chat", Messages: []importer.Message{{Role: "assistant", Content: `MARSHAL_PROPOSAL {"action":"setting","key":"acceptance-mode","value":"marshal"}`}}}
		w.observeMarshalProposals(tr)
		w.observeMarshalProposals(tr)
		batch := w.permissions.queue.Take(false)
		if len(batch) != 1 {
			t.Fatalf("popups=%d", len(batch))
		}
		text, err := permission.Render(batch)
		if err != nil || !strings.Contains(text, "/marshal settings acceptance-mode marshal") {
			t.Fatalf("popup=%s err=%v", text, err)
		}
		if err := w.decidePermission(context.Background(), batch[0], allow, "operator popup"); err != nil {
			t.Fatal(err)
		}
		settings, err := rt.Marshal().Store.GetMarshalSettings(context.Background(), rt.ProjectID())
		if err != nil {
			t.Fatal(err)
		}
		if (string(settings.Value.AcceptanceMode) == "marshal") != allow {
			t.Fatalf("settings=%+v allow=%v", settings.Value, allow)
		}
		events, err := rt.Store().ListEvents(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		decisions := 0
		for _, e := range events {
			if e.Type == "PERMISSION_DECIDED" {
				decisions++
			}
		}
		if decisions != 1 {
			t.Fatalf("decisions=%d", decisions)
		}
		w.observeMarshalProposals(tr)
		if !w.permissions.queue.Empty() {
			t.Fatal("replayed proposal stacked a popup")
		}
	}
}

func TestMarshalProposalStrictParsing(t *testing.T) {
	for _, line := range []string{
		`MARSHAL_PROPOSAL {"action":"setting","key":"acceptance-mode","value":"marshal"}`,
		`MARSHAL_PROPOSAL {"action":"setting","key":"rework-limit","value":"3"}`,
		`MARSHAL_PROPOSAL {"action":"continue","provider":"codex","path":"/tmp/earlier work"}`,
		`MARSHAL_PROPOSAL {"action":"read","path":"/tmp/work"}`,
		`MARSHAL_PROPOSAL {"action":"memory","id":"MEM-123"}`,
		`MARSHAL_PROPOSAL {"action":"approve"}`,
	} {
		if _, err := parseMarshalProposal(line); err != nil {
			t.Errorf("valid: %s: %v", line, err)
		}
	}
	for _, line := range []string{
		`MARSHAL_PROPOSAL {"action":"shell","value":"touch /tmp/x"}`,
		`MARSHAL_PROPOSAL {"action":"setting","key":"model","value":"codex"}`,
		`MARSHAL_PROPOSAL {"action":"setting","key":"acceptance-mode","value":"yes"}`,
		`MARSHAL_PROPOSAL {"action":"setting","key":"rework-limit","value":"-1"}`,
		`MARSHAL_PROPOSAL {"action":"setting","key":"ultra-concurrency","value":"0"}`,
		`MARSHAL_PROPOSAL {"action":"setting","key":"rework-limit","value":"01"}`,
		`MARSHAL_PROPOSAL {"action":"approve","extra":"x"}`,
		`MARSHAL_PROPOSAL {"action":"approve","action":"approve"}`,
		`MARSHAL_PROPOSAL {"action":"read","path":"relative"}`,
		`MARSHAL_PROPOSAL {"action":"read","path":"/tmp/\u001b"}`,
		`MARSHAL_PROPOSAL {"action":"memory","id":"a b"}`,
		`MARSHAL_PROPOSAL {"action":"approve"} {}`,
		`MARSHAL_PROPOSAL {"action":`,
	} {
		if _, err := parseMarshalProposal(line); err == nil {
			t.Errorf("accepted invalid: %s", line)
		}
	}
	w, _ := realControlWorkspace(t, "SESSION-invalid-proposals")
	w.observeMarshalProposals(importer.SessionTranscript{SessionID: "chat", Messages: []importer.Message{{Role: "user", Content: `MARSHAL_PROPOSAL {"action":"setting","key":"control","value":"strict"}`}, {Role: "assistant", Content: `MARSHAL_PROPOSAL {"action":"setting","key":"control","value":"bogus"}`}}})
	if !w.permissions.queue.Empty() {
		t.Fatal("untrusted or malformed output queued")
	}
	w.mu.RLock()
	logged := strings.Contains(w.state.LastOutput, "Ignored Marshal proposal")
	w.mu.RUnlock()
	if !logged {
		t.Fatal("invalid proposal not logged")
	}
}

func TestMarshalProposalPopupKeysAndEvidence(t *testing.T) {
	for _, key := range []string{"A", "a", "D", "\n", "\x1b", ""} {
		t.Run(fmt.Sprintf("key-%x", key), func(t *testing.T) {
			w, rt := realControlWorkspace(t, "SESSION-proposal-key")
			dir := t.TempDir()
			fake := filepath.Join(dir, "tmux")
			if err := os.WriteFile(filepath.Join(dir, "key"), []byte(key), 0600); err != nil {
				t.Fatal(err)
			}
			script := "#!/bin/bash\nif [[ $1 == display-message ]]; then printf '%%marshal\\n'; exit; fi\nif [[ $1 == display-popup ]]; then printf 'popup\\n' >> '" + dir + "/calls'; bash -c \"${@: -1}\" < '" + dir + "/key' > '" + dir + "/screen'; exit; fi\nexit 1\n"
			if err := os.WriteFile(fake, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			tmux.SetBinaryPath(fake)
			defer tmux.ResetBinaryPath()
			w.tmuxPath, w.tmuxSession = fake, "session"
			tr := importer.SessionTranscript{SessionID: "chat", Messages: []importer.Message{{Role: "assistant", Content: `MARSHAL_PROPOSAL {"action":"setting","key":"acceptance-mode","value":"marshal"}`}}}
			w.observeMarshalProposals(tr)
			w.observeMarshalProposals(tr)
			before, _ := rt.Marshal().Store.GetMarshalSettings(t.Context(), rt.ProjectID())
			if string(before.Value.AcceptanceMode) == "marshal" {
				t.Fatal("model applied before A")
			}
			w.runPermissionQueue(t.Context())
			w.observeMarshalProposals(tr)
			w.runPermissionQueue(t.Context())
			calls, err := os.ReadFile(filepath.Join(dir, "calls"))
			if err != nil || string(calls) != "popup\n" {
				t.Fatalf("calls=%q %v", calls, err)
			}
			screen, _ := os.ReadFile(filepath.Join(dir, "screen"))
			if !strings.Contains(string(screen), "/marshal settings acceptance-mode marshal") || !strings.Contains(string(screen), "A = Allow") {
				t.Fatalf("screen=%s", screen)
			}
			after, err := rt.Marshal().Store.GetMarshalSettings(t.Context(), rt.ProjectID())
			if err != nil {
				t.Fatal(err)
			}
			if (string(after.Value.AcceptanceMode) == "marshal") != (key == "A") {
				t.Fatalf("key=%q mode=%s", key, after.Value.AcceptanceMode)
			}
			events, err := rt.Store().ListEvents(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, e := range events {
				if e.Type == "PERMISSION_DECIDED" {
					count++
					if e.Data["allow"] != (key == "A") || e.Data["source"] != "operator popup" || e.Data["object"] != "/marshal settings acceptance-mode marshal" {
						t.Fatalf("evidence=%v", e.Data)
					}
				}
			}
			if count != 1 {
				t.Fatalf("decisions=%d", count)
			}
			inbox, err := os.ReadFile(inboxPath(rt.ProjectRoot(), "marshal"))
			if err != nil {
				t.Fatal(err)
			}
			decision := "declined"
			if key == "A" {
				decision = "applied"
			}
			if !strings.Contains(string(inbox), decision+": /marshal settings acceptance-mode marshal") {
				t.Fatalf("decision not delivered: %s", inbox)
			}
		})
	}
}

func TestMarshalProposalTimeoutDeclines(t *testing.T) {
	w, rt := realControlWorkspace(t, "SESSION-proposal-timeout")
	w.observeMarshalProposals(importer.SessionTranscript{SessionID: "chat", Messages: []importer.Message{{Role: "assistant", Content: `MARSHAL_PROPOSAL {"action":"setting","key":"control","value":"strict"}`}}})
	batch := w.permissions.queue.Take(false)
	dir := t.TempDir()
	fake := filepath.Join(dir, "tmux")
	script := "#!/bin/bash\n[[ $1 == display-popup ]] || exit 1\n{ sleep .2; } | bash -c \"${@: -1}\"\n"
	if err := os.WriteFile(fake, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	tmux.SetBinaryPath(fake)
	defer tmux.ResetBinaryPath()
	allow, err := permission.Popup(t.Context(), "", batch, 20*time.Millisecond)
	if err != nil || allow {
		t.Fatalf("timeout=%v %v", allow, err)
	}
	if err := w.decidePermission(t.Context(), batch[0], allow, "operator popup"); err != nil {
		t.Fatal(err)
	}
	settings, _ := rt.Marshal().Store.GetMarshalSettings(t.Context(), rt.ProjectID())
	if string(settings.Value.Control) == "strict" {
		t.Fatal("timeout applied")
	}
	events, _ := rt.Store().ListEvents(t.Context())
	found := false
	for _, e := range events {
		if e.Type == "PERMISSION_DECIDED" && e.Data["allow"] == false {
			found = true
		}
	}
	if !found {
		t.Fatal("timeout decline not recorded")
	}
}

func TestMarshalProposalBoundNativeHistory(t *testing.T) {
	w, rt := realControlWorkspace(t, "SESSION-proposal-history")
	root := rt.ProjectRoot()
	dir := t.TempDir()
	w.tmuxActiveWins = map[string]*activeTmuxAgent{"marshal-chat": {id: "marshal-chat", role: "marshal-chat", sessionID: "chat", provider: "codex"}}
	watch := newNativeHistoryWatch(dir, root)
	if _, err := w.prepareChatHistoryWatch(root, watch, "chat", []string{}); err != nil {
		t.Fatal(err)
	}
	proposal := `MARSHAL_PROPOSAL {"action":"setting","key":"control","value":"strict"}`
	for _, id := range []string{"other", "chat"} {
		data := fmt.Sprintf("{\"type\":\"session_meta\",\"payload\":{\"id\":%q,\"cwd\":%q}}\n{\"type\":\"response_item\",\"payload\":{\"type\":\"message\",\"role\":\"assistant\",\"phase\":\"final_answer\",\"content\":[{\"type\":\"output_text\",\"text\":%q}]}}\n", id, root, proposal)
		if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := watch.sync(); err != nil {
		t.Fatal(err)
	}
	if err := watch.sync(); err != nil {
		t.Fatal(err)
	}
	batch := w.permissions.queue.Take(false)
	if len(batch) != 1 || batch[0].Object != "/marshal settings control strict" {
		t.Fatalf("native history popups=%v", batch)
	}
}

func TestMarshalProposalReadMemoryAndContinuation(t *testing.T) {
	w, rt := realControlWorkspace(t, "SESSION-proposal-continuation")
	root := rt.ProjectRoot()
	folder := t.TempDir()
	data := fmt.Sprintf("{\"type\":\"session_meta\",\"timestamp\":\"2026-10-01T10:00:00Z\",\"payload\":{\"id\":\"earlier\",\"cwd\":%q}}\n{\"type\":\"response_item\",\"payload\":{\"type\":\"message\",\"role\":\"assistant\",\"phase\":\"final_answer\",\"content\":[{\"type\":\"output_text\",\"text\":\"Pending work\"}]}}\n", root)
	if err := os.WriteFile(filepath.Join(folder, "earlier.jsonl"), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	propose := func(fields map[string]string) {
		payload, _ := json.Marshal(fields)
		w.observeMarshalProposals(importer.SessionTranscript{SessionID: "chat", Messages: []importer.Message{{Role: "assistant", Content: marshalProposalPrefix + string(payload)}}})
	}
	propose(map[string]string{"action": "continue", "provider": "codex", "path": folder})
	batch := w.permissions.queue.Take(false)
	if len(batch) != 1 || rt.HasReadGrant(folder) {
		t.Fatal("continuation applied before decision")
	}
	if err := w.decidePermission(t.Context(), batch[0], true, "operator popup"); err != nil {
		t.Fatal(err)
	}
	if !rt.HasReadGrant(folder) {
		t.Fatal("continuation read not allowed")
	}
	decisions, err := rt.Store().ListEvents(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range decisions {
		if event.Type == "PERMISSION_DECIDED" && event.Data["object"] == folder {
			found = true
			if event.Data["continuation_provider"] != "codex" || event.Data["task_id"] != "" {
				t.Fatalf("continuation evidence=%v", event.Data)
			}
		}
	}
	if !found {
		t.Fatal("continuation operator evidence missing")
	}
	inbox, _ := os.ReadFile(inboxPath(root, "marshal"))
	if !strings.Contains(string(inbox), "Pending work") {
		t.Fatal("continuation not delivered")
	}
	candidates := rt.ContinuationCandidates()
	if len(candidates) != 1 {
		t.Fatalf("candidates=%d", len(candidates))
	}
	// Continuation already queues candidate permissions. A matching proposal must
	// use that same request rather than stacking a second memory popup.
	propose(map[string]string{"action": "memory", "id": candidates[0].ID})
	batch = w.permissions.queue.Take(false)
	if len(batch) != 1 || batch[0].Kind != "memory" {
		t.Fatalf("memory requests=%v", batch)
	}
	if err := w.decidePermission(t.Context(), batch[0], true, "operator popup"); err != nil {
		t.Fatal(err)
	}
	records, err := rt.Store().ListMemoryV2(t.Context(), store.MemoryQueryFilter{ProjectID: rt.ProjectID()})
	if err != nil || len(records) != 1 {
		t.Fatalf("memory=%v %v", records, err)
	}
	other := t.TempDir()
	propose(map[string]string{"action": "read", "path": other})
	batch = w.permissions.queue.Take(false)
	if len(batch) != 1 {
		t.Fatalf("read requests=%v", batch)
	}
	if err := w.decidePermission(t.Context(), batch[0], false, "operator popup"); err != nil {
		t.Fatal(err)
	}
	if rt.HasReadGrant(other) {
		t.Fatal("declined read granted")
	}
}

func TestMarshalProposalApprovalWaitsForDraftAndExpires(t *testing.T) {
	w, rt := realControlWorkspace(t, "SESSION-proposal-approval")
	tr := importer.SessionTranscript{SessionID: "chat", Messages: []importer.Message{{Role: "assistant", Content: `MARSHAL_PROPOSAL {"action":"approve"}`}}}
	w.observeMarshalProposals(tr)
	if !w.permissions.queue.Empty() {
		t.Fatal("approval popup before imported draft")
	}
	m := w.marshalSession()
	m.mu.Lock()
	m.service = rt.Marshal()
	m.runID = "RUN-proposal"
	m.mu.Unlock()
	run := marshal.Run{PlanID: "PLAN-proposal", PlanVersion: 1, State: marshal.Drafting, Settings: marshal.DefaultSettings(), Tasks: []marshal.Task{{PlanTaskID: "T1"}}}
	rec, err := rt.Marshal().Store.SetMarshalRun(t.Context(), rt.ProjectID(), "RUN-proposal", run, 0)
	if err != nil {
		t.Fatal(err)
	}
	w.queueMarshalPlanApproval()
	w.queueMarshalPlanApproval()
	w.observeMarshalProposals(tr)
	batch := w.permissions.queue.Take(false)
	if len(batch) != 1 || batch[0].Object != "/marshal approve" || !strings.Contains(batch[0].Scope, "PLAN-proposal version 1") {
		t.Fatalf("approval requests=%v", batch)
	}
	// Updating even the result state invalidates the old proposal binding.
	run.PlanVersion = 2
	if _, err := rt.Marshal().Store.SetMarshalRun(t.Context(), rt.ProjectID(), "RUN-proposal", run, rec); err != nil {
		t.Fatal(err)
	}
	if err := w.decidePermission(t.Context(), batch[0], true, "operator popup"); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("stale approval=%v", err)
	}
	m.mu.Lock()
	busy := m.busy
	m.mu.Unlock()
	if busy {
		t.Fatal("stale approval dispatched")
	}
}

func TestMarshalProposalTaskAcceptanceUsesHandler(t *testing.T) {
	w, rt := realControlWorkspace(t, "SESSION-proposal-task")
	m := w.marshalSession()
	m.mu.Lock()
	m.service = rt.Marshal()
	m.runID = "RUN-task"
	m.mu.Unlock()
	run := marshal.Run{PlanID: "PLAN-task", PlanVersion: 1, State: marshal.AwaitingUser, Settings: marshal.DefaultSettings(), Tasks: []marshal.Task{{PlanTaskID: "T.1", State: marshal.HandedIn}}}
	if _, err := rt.Marshal().Store.SetMarshalRun(t.Context(), rt.ProjectID(), "RUN-task", run, 0); err != nil {
		t.Fatal(err)
	}
	w.setMarshalPanel(newMarshalPanel("RUN-task", "codex", run, "review"))
	tr := importer.SessionTranscript{SessionID: "chat", Messages: []importer.Message{{Role: "assistant", Content: `MARSHAL_PROPOSAL {"action":"accept","id":"T.1"}`}}}
	w.observeMarshalProposals(tr)
	batch := w.permissions.queue.Take(false)
	if len(batch) != 1 || batch[0].Object != "/marshal accept T.1" {
		t.Fatalf("task popup=%v", batch)
	}
	if _, err := m.approver(t.Context(), "RUN-task", "T.1"); err == nil {
		t.Fatal("model authorized task before operator")
	}
	if err := w.decidePermission(t.Context(), batch[0], true, "operator popup"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.approver(t.Context(), "RUN-task", "T.1"); err != nil {
		t.Fatalf("same slash handler did not authorize task: %v", err)
	}
	if _, err := m.approver(t.Context(), "RUN-task", "T.1"); err == nil {
		t.Fatal("approval reused")
	}
	tr.Messages[0].Content = `MARSHAL_PROPOSAL {"action":"accept","id":"unknown"}`
	w.observeMarshalProposals(tr)
	if !w.permissions.queue.Empty() {
		t.Fatal("nonexistent task queued")
	}
}

func TestMarshalProposalSettingAllowList(t *testing.T) {
	w, rt := realControlWorkspace(t, "SESSION-proposal-settings")
	values := map[string][]string{
		"execution-rights": {"none", "read-only", "small-tasks"},
		"acceptance-mode":  {"marshal", "marshal-then-user", "user"},
		"control":          {"strict", "free"},
		"rework-limit":     {"0", "3"}, "ultra-concurrency": {"1", "5"},
		"task-tokens": {"0", "12"}, "plan-tokens": {"0", "12"},
		"task-money": {"0", "12"}, "plan-money": {"0", "12"},
		"task-wall-seconds": {"0", "12"}, "plan-wall-seconds": {"0", "12"},
	}
	for key, vs := range values {
		for _, value := range vs {
			payload, _ := json.Marshal(map[string]string{"action": "setting", "key": key, "value": value})
			w.observeMarshalProposals(importer.SessionTranscript{SessionID: "chat", Messages: []importer.Message{{Role: "assistant", Content: marshalProposalPrefix + string(payload)}}})
			batch := w.permissions.queue.Take(false)
			if len(batch) != 1 {
				t.Fatalf("%s=%s requests=%v", key, value, batch)
			}
			before, _ := rt.Marshal().Store.GetMarshalSettings(t.Context(), rt.ProjectID())
			if err := w.decidePermission(t.Context(), batch[0], true, "operator popup"); err != nil {
				t.Fatal(err)
			}
			after, _ := rt.Marshal().Store.GetMarshalSettings(t.Context(), rt.ProjectID())
			if after.Revision != before.Revision+1 {
				t.Fatal("setting did not use existing CAS handler")
			}
			output, err := w.cmd.Handle(t.Context(), "/marshal settings")
			if err != nil || !strings.Contains(output+"\n", key+" "+value+"\n") {
				t.Fatalf("%s=%s readback: %s %v", key, value, output, err)
			}
		}
	}
}

func TestMarshalProposalRejectsToolPayloadAndInvalidJSON(t *testing.T) {
	w, _ := realControlWorkspace(t, "SESSION-proposal-tool")
	proposal := `MARSHAL_PROPOSAL {"action":"setting","key":"control","value":"strict"}`
	w.observeMarshalProposals(importer.SessionTranscript{SessionID: "chat", Messages: []importer.Message{{Role: "assistant", Kind: importer.MessageKindToolUse, Content: proposal}}})
	w.observeMarshalProposals(importer.SessionTranscript{SessionID: "chat", Messages: []importer.Message{{Role: "assistant", Content: "```json\n" + proposal + "\n```"}}})
	if !w.permissions.queue.Empty() {
		t.Fatal("tool payload or fenced example queued permission")
	}
	for _, payload := range []string{`[]`, `{"action":3}`, `{"action":"approve"`, `{"action":"setting","key":"control","value":null}`, `{"action":"amend","reason":"approve"}`, `{"action":"amend","reason":"deny"}`, `{"action":"amend","reason":""}`, `{"action":"continue","provider":"gemini","path":"/tmp"}`, `{"action":"read","path":"/tmp/../tmp"}`, `{"action":"memory","id":""}`, strings.Repeat("x", 4100)} {
		if _, err := parseMarshalProposal(marshalProposalPrefix + payload); err == nil {
			t.Fatalf("accepted %s", payload)
		}
	}
	for _, payload := range []string{`{"action":"accept","id":"T1"}`, `{"action":"return","id":"T1","reason":"failed check"}`, `{"action":"amend","reason":"add caching"}`, `{"action":"close"}`, `{"action":"resume"}`} {
		if _, err := parseMarshalProposal(marshalProposalPrefix + payload); err != nil {
			t.Fatalf("rejected %s: %v", payload, err)
		}
	}
}

func TestMarshalProposalPlanApprovalUsesHandler(t *testing.T) {
	w, rt := realControlWorkspace(t, "SESSION-proposal-plan")
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	service := *rt.Marshal()
	m := w.marshalSession()
	service.ApprovalActor = m.approver
	service.Drivers = nil
	service.GovernedDrivers = nil
	service.ProbeWorker = func(context.Context, string) error { return errors.New("worker dispatch disabled in proposal fixture") }
	draft, err := service.DraftFromProposal([]byte(`{"tasks":[{"id":"T1","title":"Review fixture","criteria":["fixture checked"],"paths":["README.md"],"depends_on":[],"worker":"codex","mode":"governed","checks":[{"command":"true","criteria":["fixture checked"]}]}]}`), "codex")
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.StartPlanningFromDraft(t.Context(), "RUN-plan-proposal", "proposal fixture", draft, marshal.Budget{})
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.service = &service
	m.runID = "RUN-plan-proposal"
	m.provider = "codex"
	m.mu.Unlock()
	w.setMarshalPanel(newMarshalPanel("RUN-plan-proposal", "codex", run, "review plan"))
	w.observeMarshalProposals(importer.SessionTranscript{SessionID: "chat", Messages: []importer.Message{{Role: "assistant", Content: `MARSHAL_PROPOSAL {"action":"approve"}`}}})
	batch := w.permissions.queue.Take(false)
	if len(batch) != 1 {
		t.Fatalf("approval popup=%v", batch)
	}
	if err := w.decidePermission(t.Context(), batch[0], true, "operator popup"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		events, err := rt.Store().MarshalDecisions(t.Context(), "RUN-plan-proposal")
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, e := range events {
			if e.Type == "marshal.plan.approved" {
				found = true
			}
		}
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("plan approval handler did not persist approval; panel=%+v", w.marshalPanel())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestMarshalProposalAmendmentApprovalBinding(t *testing.T) {
	w, rt := realControlWorkspace(t, "SESSION-proposal-amend")
	m := w.marshalSession()
	run := marshal.Run{PlanID: "PLAN-amend", PlanVersion: 1, State: marshal.AwaitingUser, Settings: marshal.DefaultSettings()}
	if _, err := rt.Marshal().Store.SetMarshalRun(t.Context(), rt.ProjectID(), "RUN-amend", run, 0); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.service = rt.Marshal()
	m.runID = "RUN-amend"
	m.amended = true
	m.pending = &marshalAmendment{reason: "add a cache", planVersion: 1}
	m.mu.Unlock()
	w.observeMarshalProposals(importer.SessionTranscript{SessionID: "chat", Messages: []importer.Message{{Role: "assistant", Content: `MARSHAL_PROPOSAL {"action":"approve"}`}}})
	batch := w.permissions.queue.Take(false)
	if len(batch) != 1 || batch[0].Object != "/marshal amend approve" {
		t.Fatalf("amendment popup=%v", batch)
	}
	m.mu.Lock()
	m.pending = &marshalAmendment{reason: "different change", planVersion: 1}
	m.mu.Unlock()
	if err := w.decidePermission(t.Context(), batch[0], true, "operator popup"); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("changed amendment approved: %v", err)
	}
	m.mu.Lock()
	busy := m.busy
	m.mu.Unlock()
	if busy {
		t.Fatal("changed amendment dispatched")
	}
}
