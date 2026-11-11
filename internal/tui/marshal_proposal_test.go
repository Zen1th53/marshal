package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/statepath"
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
		w, rt := realControlWorkspace(t, "SESSION-proposal", false)
		before, err := rt.Marshal().Store.GetMarshalSettings(t.Context(), rt.ProjectID())
		if err != nil {
			t.Fatal(err)
		}
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
		if allow && (settings.Value.AcceptanceMode != marshal.AcceptMarshal || settings.Revision != before.Revision+1) || !allow && settings != before {
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
	w, _ := realControlWorkspace(t, "SESSION-invalid-proposals", false)
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
	if Edition() == "Marshal" {
		t.Skip("Marshal never opens an operator popup for proposals; the Marshal decides (TestExtremeUnverifiedProposalFileNeverAutoApplied)")
	}
	for _, key := range []string{"A", "a", "D", "\n", "\x1b", ""} {
		t.Run(fmt.Sprintf("key-%x", key), func(t *testing.T) {
			w, rt := realControlWorkspace(t, "SESSION-proposal-key", false)
			dir := t.TempDir()
			fake := filepath.Join(dir, "tmux")
			if err := os.WriteFile(filepath.Join(dir, "key"), []byte(key), 0600); err != nil {
				t.Fatal(err)
			}
			script := "#!/bin/bash\nif [[ $* == *'#{client_height} #{client_width}'* ]]; then echo '40 120'; exit; fi\nif [[ $1 == list-clients ]]; then printf 'client|%%marshal|session\\n'; exit; fi\nif [[ $1 == display-message ]]; then printf '%%marshal\\n'; exit; fi\nif [[ $1 == display-popup ]]; then printf 'popup\\n' >> '" + dir + "/calls'; bash -c \"${@: -1}\" < '" + dir + "/key' > '" + dir + "/screen'; exit; fi\nexit 1\n"
			if err := os.WriteFile(fake, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			tmux.SetBinaryPath(fake)
			defer tmux.ResetBinaryPath()
			w.tmuxPath, w.tmuxSession = fake, "session"
			w.tmuxMarshalPaneID = "%marshal"
			tr := importer.SessionTranscript{SessionID: "chat", Messages: []importer.Message{{Role: "assistant", Content: `MARSHAL_PROPOSAL {"action":"setting","key":"acceptance-mode","value":"marshal"}`}}}
			w.observeMarshalProposals(tr)
			w.observeMarshalProposals(tr)
			before, _ := rt.Marshal().Store.GetMarshalSettings(t.Context(), rt.ProjectID())
			if before.Revision != 0 {
				t.Fatal("model applied before A")
			}
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			w.runPermissionQueue(ctx)
			w.observeMarshalProposals(tr)
			w.runPermissionQueue(ctx)
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
			if key == "A" && (after.Value.AcceptanceMode != marshal.AcceptMarshal || after.Revision != before.Revision+1) || key != "A" && after != before {
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
	w, rt := realControlWorkspace(t, "SESSION-proposal-timeout", false)
	w.observeMarshalProposals(importer.SessionTranscript{SessionID: "chat", Messages: []importer.Message{{Role: "assistant", Content: `MARSHAL_PROPOSAL {"action":"setting","key":"control","value":"strict"}`}}})
	batch := w.permissions.queue.Take(false)
	dir := t.TempDir()
	fake := filepath.Join(dir, "tmux")
	script := "#!/bin/bash\nif [[ $* == *'#{client_height} #{client_width}'* ]]; then echo '40 120'; exit; fi\n[[ $1 == display-popup ]] || exit 1\n{ sleep .2; } | bash -c \"${@: -1}\"\n"
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
	message := importer.Message{Role: "assistant", Content: `MARSHAL_PROPOSAL {"action":"setting","key":"control","value":"strict"}`}
	w.observeMarshalProposals(importer.SessionTranscript{SessionID: "chat", Messages: []importer.Message{message, message}})
	if len(w.permissions.queue.Take(false)) != 1 {
		t.Fatal("timeout suppressed later identical emission")
	}
}

func TestMarshalProposalBoundNativeHistory(t *testing.T) {
	w, rt := realControlWorkspace(t, "SESSION-proposal-history", false)
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
	if Edition() == "Marshal" {
		t.Skip("Marshal applies reads automatically and waits for the Marshal on memory; TestExtremeReadsApplyAutomatically covers it")
	}
	w, rt := realControlWorkspace(t, "SESSION-proposal-continuation", false)
	// The launch scan found this project's Codex history folder; only that
	// folder may be proposed. Popup/grant assertions below remain unchanged.
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := rt.ProjectRoot()
	folder := rt.EarlierWorkFolder("codex")
	if err := os.MkdirAll(folder, 0700); err != nil {
		t.Fatal(err)
	}
	w.launch.mu.Lock()
	w.launch.scan = projectScan{Sessions: []scanSessions{{Provider: "codex", NeedsGrant: true}, {Provider: "claude", NeedsGrant: true}}}
	w.launch.mu.Unlock()
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
	// A read outside this project's history folders is deferred, never queued.
	propose(map[string]string{"action": "read", "path": t.TempDir()})
	if !w.permissions.queue.Empty() {
		t.Fatal("read outside this project's history folders was queued")
	}
	other := rt.EarlierWorkFolder("claude")
	if err := os.MkdirAll(other, 0700); err != nil {
		t.Fatal(err)
	}
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
	fixture := newMarshalPopupFixture(t, false, false)
	w, rt := fixture.w, fixture.runtime
	m := w.marshalSession()
	m.mu.Lock()
	runID := m.runID
	m.runID = "" // The imported proposal precedes attachment to its saved draft.
	m.mu.Unlock()
	tr := importer.SessionTranscript{SessionID: "chat", Messages: []importer.Message{{Role: "assistant", Content: `MARSHAL_PROPOSAL {"action":"approve"}`}}}
	w.observeMarshalProposals(tr)
	if !w.permissions.queue.Empty() {
		t.Fatal("approval popup before imported draft")
	}
	m.mu.Lock()
	m.runID = runID
	m.mu.Unlock()
	record, err := fixture.service.Store.GetMarshalRun(t.Context(), rt.ProjectID(), runID)
	if err != nil {
		t.Fatal(err)
	}
	w.queueMarshalPlanApproval()
	w.queueMarshalPlanApproval()
	w.observeMarshalProposals(tr)
	batch := w.permissions.queue.Take(false)
	if len(batch) != 1 || batch[0].Object != "/marshal approve" || !strings.Contains(batch[0].Detail, record.Value.PlanID+" version 1") {
		t.Fatalf("approval requests=%v", batch)
	}
	// Updating even the result state invalidates the old proposal binding.
	run := record.Value
	run.PlanVersion = 2
	if _, err := fixture.service.Store.SetMarshalRun(t.Context(), rt.ProjectID(), runID, run, record.Revision); err != nil {
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
	w, rt := realControlWorkspace(t, "SESSION-proposal-task", false)
	m := w.marshalSession()
	m.mu.Lock()
	m.service = rt.Marshal()
	m.runID = "RUN-task"
	m.mu.Unlock()
	run := marshal.Run{PlanID: "PLAN-task", PlanVersion: 1, State: marshal.AwaitingUser, Settings: marshal.DefaultSettings(), Tasks: []marshal.Task{{PlanTaskID: "T.1", State: marshal.HandedIn, ResultCommit: "result"}}}
	if _, err := rt.Marshal().Store.SetMarshalRun(t.Context(), rt.ProjectID(), "RUN-task", run, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Marshal().Store.SetMarshalTask(t.Context(), "RUN-task", run.Tasks[0], 0); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Marshal().Store.SetMarshalHandIn(t.Context(), "RUN-task", "T.1", 1, marshal.HandIn{ResultCommit: "result"}); err != nil {
		t.Fatal(err)
	}
	purpose, err := rt.Marshal().TaskAcceptance(t.Context(), "RUN-task", "T.1")
	if err != nil {
		t.Fatal(err)
	}
	w.setMarshalPanel(newMarshalPanel("RUN-task", "codex", run, "review"))
	tr := importer.SessionTranscript{SessionID: "chat", Messages: []importer.Message{{Role: "assistant", Content: `MARSHAL_PROPOSAL {"action":"accept","id":"T.1"}`}}}
	w.observeMarshalProposals(tr)
	batch := w.permissions.queue.Take(false)
	if len(batch) != 1 || batch[0].Object != "/marshal accept T.1" {
		t.Fatalf("task popup=%v", batch)
	}
	if _, err := m.approver(t.Context(), "RUN-task", purpose); err == nil {
		t.Fatal("model authorized task before operator")
	}
	if err := w.decidePermission(t.Context(), batch[0], true, "operator popup"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.approver(t.Context(), "RUN-task", purpose); err != nil {
		t.Fatalf("same slash handler did not authorize task: %v", err)
	}
	if _, err := m.approver(t.Context(), "RUN-task", purpose); err == nil {
		t.Fatal("approval reused")
	}
	tr.Messages[0].Content = `MARSHAL_PROPOSAL {"action":"accept","id":"unknown"}`
	w.observeMarshalProposals(tr)
	if !w.permissions.queue.Empty() {
		t.Fatal("nonexistent task queued")
	}
}

func TestMarshalProposalSettingAllowList(t *testing.T) {
	w, rt := realControlWorkspace(t, "SESSION-proposal-settings", false)
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
	w, _ := realControlWorkspace(t, "SESSION-proposal-tool", false)
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
	fixture := newMarshalPopupFixture(t, false, false)
	w, rt := fixture.w, fixture.runtime
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
	fixture := newMarshalPopupFixture(t, false, false)
	w, rt := fixture.w, fixture.runtime
	m := w.marshalSession()
	m.mu.Lock()
	runID := m.runID
	m.mu.Unlock()
	record, err := fixture.service.Store.GetMarshalRun(t.Context(), rt.ProjectID(), runID)
	if err != nil {
		t.Fatal(err)
	}
	run := record.Value
	run.State = marshal.AwaitingUser
	if _, err := fixture.service.Store.SetMarshalRun(t.Context(), rt.ProjectID(), runID, run, record.Revision); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.amended = true
	m.pending = &marshalAmendment{reason: "add a cache", planVersion: run.PlanVersion}
	m.mu.Unlock()
	w.observeMarshalProposals(importer.SessionTranscript{SessionID: "chat", Messages: []importer.Message{{Role: "assistant", Content: `MARSHAL_PROPOSAL {"action":"approve"}`}}})
	batch := w.permissions.queue.Take(false)
	if len(batch) != 1 || batch[0].Object != "/marshal amend approve" {
		t.Fatalf("amendment popup=%v", batch)
	}
	m.mu.Lock()
	m.pending = &marshalAmendment{reason: "different change", planVersion: run.PlanVersion}
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

func TestMarshalProposalReemittedAfterDecision(t *testing.T) {
	for _, allow := range []bool{true, false} {
		w, _ := realControlWorkspace(t, "SESSION-retry", false)
		msg := importer.Message{Role: "assistant", Content: `MARSHAL_PROPOSAL {"action":"setting","key":"control","value":"strict"}`}
		tr := importer.SessionTranscript{SessionID: "chat", Messages: []importer.Message{msg}}
		w.observeMarshalProposals(tr)
		batch := w.permissions.queue.Take(false)
		tr.Messages = append(tr.Messages, msg)
		w.observeMarshalProposals(tr)
		if !w.permissions.queue.Empty() {
			t.Fatal("duplicate pending popup")
		}
		if err := w.decidePermission(t.Context(), batch[0], allow, "operator popup"); err != nil {
			t.Fatal(err)
		}
		w.observeMarshalProposals(tr)
		if !w.permissions.queue.Empty() {
			t.Fatal("old message replayed")
		}
		tr.Messages = append(tr.Messages, msg)
		w.observeMarshalProposals(tr)
		if got := w.permissions.queue.Take(false); len(got) != 1 {
			t.Fatalf("fresh identical proposal: %v", got)
		}
	}
}

func TestMarshalProposalFileHandoff(t *testing.T) {
	w, rt := realControlWorkspace(t, "SESSION-files", false)
	root := rt.ProjectRoot()
	dir := filepath.Join(root, statepath.DirName, "proposals")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	valid := `{"action":"setting","key":"control","value":"strict"}`
	for name, data := range map[string]string{"valid.json": valid, "invalid.json": `{"action":"shell","value":"bad"}`, "duplicate.json": `{"action":"approve","action":"close"}`, "large.json": strings.Repeat(" ", 4097)} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	external := filepath.Join(t.TempDir(), "outside")
	os.WriteFile(external, []byte(valid), 0600)
	if err := os.Symlink(external, filepath.Join(dir, "link.json")); err != nil {
		t.Fatal(err)
	}
	w.observeMarshalProposalFiles(root)
	w.observeMarshalProposalFiles(root)
	batch := w.permissions.queue.Take(false)
	if len(batch) != 1 || batch[0].Object != "/marshal settings control strict" {
		t.Fatalf("requests=%v", batch)
	}
	before, _ := rt.Marshal().Store.GetMarshalSettings(t.Context(), rt.ProjectID())
	if string(before.Value.Control) == "strict" {
		t.Fatal("file conferred authority")
	}
	if err := w.decidePermission(t.Context(), batch[0], false, "operator popup"); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "retry.json"), []byte(valid), 0600)
	w.observeMarshalProposalFiles(root)
	if len(w.permissions.queue.Take(false)) != 1 {
		t.Fatal("file retry suppressed")
	}
	data, _ := os.ReadFile(external)
	if string(data) != valid {
		t.Fatal("external symlink target modified")
	}
}

func TestMarshalProposalNavigationTargets(t *testing.T) {
	w, _ := realControlWorkspace(t, "SESSION-nav-targets", false)
	dir := t.TempDir()
	fake := filepath.Join(dir, "tmux")
	log := filepath.Join(dir, "calls")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> '"+log+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	tmux.SetBinaryPath(fake)
	defer tmux.ResetBinaryPath()
	w.tmuxMarshalPaneID = "%centre"
	for i, provider := range []string{"codex", "claude", "opencode", "antigravity"} {
		pane := fmt.Sprintf("%%%d", i)
		w.tmuxActiveWins = map[string]*activeTmuxAgent{provider: {provider: provider, paneID: pane, role: "worker"}}
		key := []string{"F7", "F8", "F9", "F12"}[i]
		w.navigatePermissionPopup(t.Context(), "client", key)
		data, _ := os.ReadFile(log)
		if !strings.Contains(string(data), "switch-client -c client -t "+pane) {
			t.Fatalf("%s did not navigate: %s", key, data)
		}
	}
}

func TestMarshalIntakeFilesPersistValidatedPreferences(t *testing.T) {
	w, rt := realControlWorkspace(t, "SESSION-file-intake")
	dir := filepath.Join(rt.ProjectRoot(), statepath.DirName, "proposals")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	emit := func(name, data string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name+".json"), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		w.observeMarshalProposalFiles(rt.ProjectRoot())
	}
	emit("01", `{"action":"intake","language":"Uzbek"}`)
	emit("02", `{"action":"intake","language":"English"}`)
	for i, bad := range []string{
		`{"action":"intake","language":"English","earlier_work":"no"}`,
		`{"action":"intake","language":"Bad","command":"approve"}`,
		`{"action":"intake","language":"Bad","language":"Other"}`,
		`{"action":"intake","language":""}`,
		`{"action":"intake","language":"Bad\nInjected"}`,
	} {
		emit(fmt.Sprintf("bad-%d", i), bad)
	}
	// Replaying an old transcript must not overwrite the newer file preferences.
	w.observeMarshalProposals(importer.SessionTranscript{Messages: []importer.Message{{Role: "assistant", Content: `MARSHAL_INTAKE {"language":"Old"}`}}})
	fresh := NewWorkspace(nil, "project", "session")
	brief := fresh.marshalContinuityBriefing(rt.ProjectRoot(), "protocol")
	if !strings.Contains(brief, `"language":"English"`) {
		t.Fatalf("saved intake lost or invalid file accepted: %s", brief)
	}
	if !w.permissions.queue.Empty() {
		t.Fatal("intake conferred permission authority")
	}
	for _, provider := range []string{"codex", "claude", "opencode", "antigravity"} {
		args := marshalKickoffArgs(provider, rt.ProjectRoot())
		opening := args[len(args)-1]
		if opening != marshalKickoffContinue || strings.Contains(opening, `"language":"English"`) || strings.Contains(opening, "Do not repeat") {
			t.Fatalf("%s reopened/switched opening leaked file intake into chat: %s", provider, opening)
		}
		pArgs, _, dir, err := prepareMarshalLaunch(provider, rt.ProjectRoot(), nil, "protocol", false)
		if dir != nil {
			defer dir.remove()
		}
		if err != nil {
			t.Fatalf("%s prepareMarshalLaunch failed: %v", provider, err)
		}
		if pArgs[len(pArgs)-1] != marshalKickoffContinue {
			t.Fatalf("%s prepareMarshalLaunch opening = %q, want %q", provider, pArgs[len(pArgs)-1], marshalKickoffContinue)
		}
	}
	if other := fresh.marshalContinuityBriefing(t.TempDir(), "protocol"); other != "protocol" {
		t.Fatal("intake leaked into another project")
	}
	emit("03", `{"action":"intake","language":"Uzbek"}`)
}

func emitMarshalIntakeFile(t *testing.T, w *Workspace, language string) {
	t.Helper()
	dir := filepath.Join(w.runtime.ProjectRoot(), statepath.DirName, "proposals")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	file, err := os.CreateTemp(dir, "intake-*.json")
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]string{"action": "intake", "language": language})
	if err != nil {
		t.Fatal(err)
	}
	_, err = file.Write(data)
	closeErr := file.Close()
	if err != nil {
		t.Fatal(err)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	w.observeMarshalProposalFiles(w.runtime.ProjectRoot())
}

func TestPendingProposalReturnsAfterWorkspaceRestart(t *testing.T) {
	w, rt := realControlWorkspace(t, "SESSION-recovery", false)
	dir := filepath.Join(rt.ProjectRoot(), statepath.DirName, "proposals")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pending.json"), []byte(`{"action":"setting","key":"control","value":"strict"}`), 0600); err != nil {
		t.Fatal(err)
	}
	w.observeMarshalProposalFiles(rt.ProjectRoot())
	if got := w.permissions.queue.Take(false); len(got) != 1 {
		t.Fatalf("first admission: %v", got)
	}
	restarted := NewWorkspace(rt.Store(), rt.ProjectID(), "SESSION-restarted")
	restarted.runtime = rt
	restarted.observeMarshalProposalFiles(rt.ProjectRoot())
	batch := restarted.permissions.queue.Take(false)
	if len(batch) != 1 || batch[0].Object != "/marshal settings control strict" {
		t.Fatalf("lost pending proposal: %v", batch)
	}
}

func TestProposalAdmissionBeforeConsumptionAndDurableDecision(t *testing.T) {
	w, rt := realControlWorkspace(t, "SESSION-admission", false)
	line := `MARSHAL_PROPOSAL {"action":"setting","key":"control","value":"strict"}`
	// Simulate interruption just after admission, before a queue or source rename.
	if _, err := rt.Store().AdmitMarshalProposal(t.Context(), rt.ProjectID(), "interrupted-read", line); err != nil {
		t.Fatal(err)
	}
	w.observeMarshalProposalFiles(rt.ProjectRoot())
	batch := w.permissions.queue.Take(false)
	if len(batch) != 1 || batch[0].ProposalID != "interrupted-read" {
		t.Fatalf("unbound admission lost: %v", batch)
	}
	if err := w.decidePermission(t.Context(), batch[0], false, "operator popup"); err != nil {
		t.Fatal(err)
	}
	restarted := NewWorkspace(rt.Store(), rt.ProjectID(), "SESSION-restart")
	restarted.runtime = rt
	restarted.observeMarshalProposalFiles(rt.ProjectRoot())
	if !restarted.permissions.queue.Empty() {
		t.Fatal("resolved admission replayed after restart")
	}
	// The same text emitted again is retained as a distinct occurrence.
	if _, err := rt.Store().AdmitMarshalProposal(t.Context(), rt.ProjectID(), "new-occurrence", line); err != nil {
		t.Fatal(err)
	}
	restarted.observeMarshalProposalFiles(rt.ProjectRoot())
	if batch := restarted.permissions.queue.Take(false); len(batch) != 1 || batch[0].ProposalID != "new-occurrence" {
		t.Fatalf("new occurrence suppressed: %v", batch)
	}
}

func TestFailedIntakeAdmissionRemainsRecoverable(t *testing.T) {
	w, rt := realControlWorkspace(t, "SESSION-intake-recovery", false)
	root := rt.ProjectRoot()
	dir := filepath.Join(root, statepath.DirName, "proposals")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, statepath.DirName, "marshal-intake.json")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "intake.json"), []byte(`{"action":"intake","language":"English"}`), 0600); err != nil {
		t.Fatal(err)
	}
	w.observeMarshalProposalFiles(root)
	pending, err := rt.Store().PendingMarshalProposals(t.Context(), rt.ProjectID())
	if err != nil || len(pending) != 1 {
		t.Fatalf("failed intake lost: %v %v", pending, err)
	}
	if err = os.Remove(target); err != nil {
		t.Fatal(err)
	}
	w.observeMarshalProposalFiles(root)
	data, err := os.ReadFile(target)
	if err != nil || !strings.Contains(string(data), "English") {
		t.Fatalf("intake retry: %s %v", data, err)
	}
	pending, err = rt.Store().PendingMarshalProposals(t.Context(), rt.ProjectID())
	if err != nil || len(pending) != 0 {
		t.Fatalf("successful intake still pending: %v %v", pending, err)
	}
}

func TestMarshalPlanPopupFreezesPackAndAsksDeliverySeparately(t *testing.T) {
	fixture := newMarshalPopupFixture(t, true, true)
	w, service, dir, pack := fixture.w, fixture.service, fixture.dir, fixture.pack
	m, run := w.marshalSession(), fixture.run
	var err error
	w.observeMarshalProposals(importer.SessionTranscript{SessionID: "chat", Messages: []importer.Message{{Role: "assistant", Content: `MARSHAL_PROPOSAL {"action":"approve"}`}}})
	batch := w.permissions.queue.Take(false)
	if len(batch) != 1 {
		t.Fatalf("approval popup=%v", batch)
	}
	if !strings.Contains(batch[0].Detail, pack.Digest) || !strings.Contains(batch[0].Detail, "refs/heads/main") {
		t.Fatalf("popup omits binding: %+v", batch)
	}
	// Recover the persisted popup, preserving the reviewed bytes rather than
	// taking a fresh snapshot of whatever happens to be on disk now.
	original := batch[0]
	if err = os.WriteFile(filepath.Join(dir, "tasks/T1.md"), []byte("changed note"), 0600); err != nil {
		t.Fatal(err)
	}
	w.permissions.mu.Lock()
	w.permissions.runProposals = nil
	w.permissions.outstanding = nil
	w.permissions.mu.Unlock()
	w.recoverMarshalProposals()
	batch = w.permissions.queue.Take(false)
	if len(batch) != 1 || batch[0].ApprovalBinding != original.ApprovalBinding {
		t.Fatalf("recovery refreshed reviewed snapshot: %v", batch)
	}
	if err = w.applyMarshalProposal(t.Context(), batch[0]); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		m.mu.Lock()
		busy := m.busy
		m.mu.Unlock()
		if !busy {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("approval did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// The reviewed pack lives in SQLite; a later file edit cannot change what
	// is approved, so approval proceeds with the frozen bytes.
	run, err = service.Snapshot(t.Context(), "RUN-plan-proposal")
	if err != nil || run.Pack == nil || run.Pack.Digest != pack.Digest || run.Pack.Tasks["T1"] == "changed note" {
		t.Fatalf("approval did not keep the reviewed SQLite pack: %+v %v", run, err)
	}
	run, err = service.Snapshot(t.Context(), "RUN-plan-proposal")
	if err != nil || run.ApprovalScopeDigest == "" || run.ValidCloseAuthorization() {
		t.Fatalf("plan approval granted standing consent: %+v %v", run, err)
	}
	batch = w.permissions.queue.Take(false)
	if len(batch) != 1 || batch[0].Object != "/marshal deliver" || !strings.Contains(batch[0].Scope, "refs/heads/main") {
		t.Fatalf("separate delivery popup: %v", batch)
	}
	if err = w.applyMarshalProposal(t.Context(), batch[0]); err != nil {
		t.Fatal(err)
	}
	run, err = service.Snapshot(t.Context(), "RUN-plan-proposal")
	if err != nil || !run.ValidCloseAuthorization() {
		t.Fatalf("delivery consent missing: %+v %v", run, err)
	}
}

// TestPopupScopeHasNoIDs: the approval popup's Scope is a plain sentence; the
// run and plan identities live in Detail, which is never rendered.
func TestPopupScopeHasNoIDs(t *testing.T) {
	fixture := newMarshalPopupFixture(t, false, false)
	w, run := fixture.w, fixture.run
	w.observeMarshalProposals(importer.SessionTranscript{SessionID: "chat", Messages: []importer.Message{{Role: "assistant", Content: `MARSHAL_PROPOSAL {"action":"approve"}`}}})
	batch := w.permissions.queue.Take(false)
	if len(batch) != 1 || batch[0].Object != "/marshal approve" {
		t.Fatalf("approval popup=%v", batch)
	}
	req := batch[0]
	text, err := permission.Render([]permission.Request{req})
	if err != nil {
		t.Fatal(err)
	}
	wantScope := fmt.Sprintf("The plan the Marshal drafted (version %d).", run.PlanVersion)
	if !strings.Contains(text, wantScope) {
		t.Fatalf("rendered popup lacks plain scope %q:\n%s", wantScope, text)
	}
	for _, bad := range []string{"RUN-", "PLAN-"} {
		if strings.Contains(text, bad) {
			t.Fatalf("rendered popup leaks %q:\n%s", bad, text)
		}
	}
	if !strings.Contains(req.Detail, run.PlanID) || !strings.Contains(req.Detail, "RUN-plan-proposal") {
		t.Fatalf("Detail missing run/plan ids: %q", req.Detail)
	}
}

func TestStandingDeliveryPopupSurvivesRestart(t *testing.T) {
	for _, renewed := range []bool{false, true} {
		t.Run(fmt.Sprintf("renewed-%t", renewed), func(t *testing.T) {
			w, rt := realControlWorkspace(t, "SESSION-delivery-recovery", false)
			m := w.marshalSession()
			m.service, m.runID = rt.Marshal(), "RUN-delivery"
			run := marshal.Run{PlanID: "PLAN-delivery", PlanVersion: 2, State: marshal.Approved, ApprovalScopeDigest: "displayed-scope", Repository: "repository", BaseCommit: "base", TargetRef: "refs/heads/main", Settings: marshal.DefaultSettings()}
			if renewed {
				run.CloseAuthorization = &marshal.CloseAuthorization{User: "operator", ApprovalScopeDigest: "old-scope", Voided: true}
			}
			if _, err := m.service.Store.SetMarshalRun(t.Context(), rt.ProjectID(), m.runID, run, 0); err != nil {
				t.Fatal(err)
			}
			if err := w.queueMarshalRunProposal(marshalProposal{action: "deliver"}); err != nil {
				t.Fatal(err)
			}
			batch := w.permissions.queue.Take(false)
			if len(batch) != 1 {
				t.Fatalf("initial popup: %v", batch)
			}
			original := batch[0]
			w.permissions.mu.Lock()
			w.permissions.runProposals, w.permissions.outstanding = nil, nil
			w.permissions.mu.Unlock()
			w.recoverMarshalProposals()
			batch = w.permissions.queue.Take(false)
			if len(batch) != 1 || batch[0] != original {
				t.Fatalf("lost delivery confirmation: %v", batch)
			}
			w.permissions.mu.Lock()
			_, recovered := w.permissions.runProposals[batch[0].Key()]
			w.permissions.mu.Unlock()
			if !recovered {
				t.Fatal("confirmation has no recovered action")
			}
		})
	}
}

func TestMarshalProposalAmendApproveReassignHarnessVerify(t *testing.T) {
	for _, tc := range []struct {
		line    string
		action  string
		command string
	}{
		{
			line:    `MARSHAL_PROPOSAL {"action":"amend-approve"}`,
			action:  "amend-approve",
			command: "/marshal amend approve",
		},
		{
			line:    `MARSHAL_PROPOSAL {"action":"amend-deny"}`,
			action:  "amend-deny",
			command: "/marshal amend deny",
		},
		{
			line:    `MARSHAL_PROPOSAL {"action":"retry","task_id":"T.1","fresh_session":false}`,
			action:  "retry",
			command: "/marshal retry T.1",
		},
		{
			line:    `MARSHAL_PROPOSAL {"action":"retry","task_id":"T.1","fresh_session":true}`,
			action:  "retry",
			command: "/marshal retry T.1 fresh-session",
		},
		{
			line:    `MARSHAL_PROPOSAL {"action":"reassign","task_id":"T.1","target_worker":"claude"}`,
			action:  "reassign",
			command: "/marshal reassign T.1 claude",
		},
		{
			line:    `MARSHAL_PROPOSAL {"action":"harness-verify","provider":"agy"}`,
			action:  "harness-verify",
			command: "/harness verify agy",
		},
		{
			line:    `MARSHAL_PROPOSAL {"action":"harness-verify","provider":"agy","model":"gemini-3.8-flash-high"}`,
			action:  "harness-verify",
			command: "/harness verify agy gemini-3.8-flash-high",
		},
	} {
		p, err := parseMarshalProposal(tc.line)
		if err != nil {
			t.Fatalf("parse %s: %v", tc.line, err)
		}
		if p.action != tc.action {
			t.Errorf("action got %q, want %q", p.action, tc.action)
		}
		if p.command() != tc.command {
			t.Errorf("command got %q, want %q", p.command(), tc.command)
		}
	}

	for _, invalid := range []string{
		`MARSHAL_PROPOSAL {"action":"amend-deny","extra":"x"}`,
		`MARSHAL_PROPOSAL {"action":"amend-deny","reason":"foo"}`,
		`MARSHAL_PROPOSAL {"action":"retry"}`,
		`MARSHAL_PROPOSAL {"action":"retry","task_id":"T.1"}`,
		`MARSHAL_PROPOSAL {"action":"retry","task_id":""}`,
		`MARSHAL_PROPOSAL {"action":"retry","task_id":"","fresh_session":true}`,
		`MARSHAL_PROPOSAL {"action":"retry","task_id":"T 1","fresh_session":true}`,
		`MARSHAL_PROPOSAL {"action":"retry","task_id":"T.1","fresh_session":"true"}`,
		`MARSHAL_PROPOSAL {"action":"retry","task_id":"T.1","fresh_session":1}`,
		`MARSHAL_PROPOSAL {"action":"retry","task_id":"T.1","fresh_session":true,"extra":"x"}`,
		`MARSHAL_PROPOSAL {"action":"reassign"}`,
		`MARSHAL_PROPOSAL {"action":"reassign","task_id":"T.1"}`,
		`MARSHAL_PROPOSAL {"action":"reassign","task_id":"","target_worker":"claude"}`,
		`MARSHAL_PROPOSAL {"action":"reassign","task_id":"T.1","target_worker":""}`,
		`MARSHAL_PROPOSAL {"action":"reassign","task_id":"T.1","target_worker":"claude","extra":"x"}`,
		`MARSHAL_PROPOSAL {"action":"harness-verify"}`,
		`MARSHAL_PROPOSAL {"action":"harness-verify","provider":""}`,
		`MARSHAL_PROPOSAL {"action":"harness-verify","provider":"agy","extra":"x"}`,
	} {
		if _, err := parseMarshalProposal(invalid); err == nil {
			t.Errorf("expected rejection for invalid proposal: %s", invalid)
		}
	}
}

func TestMarshalProposalReassignRouting(t *testing.T) {
	fixture := newMarshalPopupFixture(t, false, false)
	w, service := fixture.w, fixture.service
	m := w.marshalSession()
	if service.GovernedDrivers == nil {
		service.GovernedDrivers = map[string]driver.Driver{}
	}
	service.GovernedDrivers["claude"] = driver.Governed{Provider: "claude", Run: func(context.Context, driver.Request) ([]marshal.CommandRecord, error) {
		return nil, nil
	}}
	const runID, taskID = "RUN-plan-proposal", "T1"
	m.grant(runID, "plan")
	run, err := service.Approve(t.Context(), runID)
	if err != nil {
		t.Fatal(err)
	}
	w.setMarshalPanel(newMarshalPanel(runID, "codex", run, "active"))
	if err := service.Escalate(t.Context(), runID, taskID, marshal.CodeEscalated, "worker rework limit reached"); err != nil {
		t.Fatal(err)
	}

	tr := importer.SessionTranscript{SessionID: "chat", Messages: []importer.Message{{
		Role:    "assistant",
		Content: `MARSHAL_PROPOSAL {"action":"reassign","task_id":"` + taskID + `","target_worker":"claude"}`,
	}}}
	w.observeMarshalProposals(tr)
	batch := w.permissions.queue.Take(false)
	if len(batch) != 1 || batch[0].Object != "/marshal reassign "+taskID+" claude" {
		t.Fatalf("unexpected queued permission: %+v", batch)
	}
	if err := w.decidePermission(t.Context(), batch[0], true, "operator popup"); err != nil {
		t.Fatal(err)
	}

	updated, err := service.Store.GetMarshalTask(t.Context(), runID, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Value.Worker != "claude" {
		t.Fatalf("task was not reassigned to claude: %+v", updated.Value)
	}
}

func TestMarshalProposalHarnessVerifyQueuesCommand(t *testing.T) {
	w, _ := realControlWorkspace(t, "SESSION-proposal-harness", false)
	tr := importer.SessionTranscript{SessionID: "chat", Messages: []importer.Message{{
		Role:    "assistant",
		Content: `MARSHAL_PROPOSAL {"action":"harness-verify","provider":"agy"}`,
	}}}
	w.observeMarshalProposals(tr)
	batch := w.permissions.queue.Take(false)
	if len(batch) != 1 || batch[0].Object != "/harness verify agy" || batch[0].Kind != "marshal-command" {
		t.Fatalf("unexpected harness verify batch: %+v", batch)
	}
}

func TestMarshalProposalRetryRouting(t *testing.T) {
	fixture := newMarshalPopupFixture(t, false, false)
	w, service := fixture.w, fixture.service
	m := w.marshalSession()
	if service.GovernedDrivers == nil {
		service.GovernedDrivers = map[string]driver.Driver{}
	}
	service.GovernedDrivers["claude"] = driver.Governed{Provider: "claude", Run: func(context.Context, driver.Request) ([]marshal.CommandRecord, error) {
		return nil, nil
	}}
	const runID, taskID = "RUN-plan-proposal", "T1"
	m.grant(runID, "plan")
	run, err := service.Approve(t.Context(), runID)
	if err != nil {
		t.Fatal(err)
	}
	w.setMarshalPanel(newMarshalPanel(runID, "codex", run, "active"))
	if err := service.Escalate(t.Context(), runID, taskID, marshal.CodeEscalated, "worker rework limit reached"); err != nil {
		t.Fatal(err)
	}

	// 1. Owner denial must never apply retry.
	trDeny := importer.SessionTranscript{SessionID: "chat-deny", Messages: []importer.Message{{
		Role:    "assistant",
		Content: `MARSHAL_PROPOSAL {"action":"retry","task_id":"` + taskID + `","fresh_session":false}`,
	}}}
	w.observeMarshalProposals(trDeny)
	batchDeny := w.permissions.queue.Take(false)
	if len(batchDeny) != 1 || batchDeny[0].Object != "/marshal retry "+taskID {
		t.Fatalf("unexpected queued permission for retry: %+v", batchDeny)
	}
	if err := w.decidePermission(t.Context(), batchDeny[0], false, "operator popup"); err != nil {
		t.Fatal(err)
	}
	afterDeny, err := service.Store.GetMarshalTask(t.Context(), runID, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if afterDeny.Value.State != marshal.Escalated {
		t.Fatalf("task was retried despite owner denial: %+v", afterDeny.Value)
	}

	// 2. Owner approval with fresh_session: true.
	tr := importer.SessionTranscript{SessionID: "chat", Messages: []importer.Message{{
		Role:    "assistant",
		Content: `MARSHAL_PROPOSAL {"action":"retry","task_id":"` + taskID + `","fresh_session":true}`,
	}}}
	w.observeMarshalProposals(tr)
	batch := w.permissions.queue.Take(false)
	if len(batch) != 1 || batch[0].Object != "/marshal retry "+taskID+" fresh-session" {
		t.Fatalf("unexpected queued permission: %+v", batch)
	}
	if err := w.decidePermission(t.Context(), batch[0], true, "operator popup"); err != nil {
		t.Fatal(err)
	}

	updated, err := service.Store.GetMarshalTask(t.Context(), runID, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Value.State != marshal.Returned {
		t.Fatalf("task was not retried: %+v", updated.Value)
	}
	if updated.Value.FreshSessionRetries != 1 {
		t.Fatalf("fresh session retries not incremented: %+v", updated.Value)
	}
}

func TestMarshalProposalAmendDenyRouting(t *testing.T) {
	fixture := newMarshalPopupFixture(t, false, false)
	w, service := fixture.w, fixture.service
	m := w.marshalSession()
	const runID = "RUN-plan-proposal"
	m.grant(runID, "plan")
	run, err := service.Approve(t.Context(), runID)
	if err != nil {
		t.Fatal(err)
	}
	w.setMarshalPanel(newMarshalPanel(runID, "codex", run, "active"))

	m.mu.Lock()
	m.amended = true
	m.pending = &marshalAmendment{reason: "add new task", planVersion: run.PlanVersion}
	m.mu.Unlock()

	// 1. Owner denial must leave amendment pending.
	trDenial := importer.SessionTranscript{SessionID: "chat-deny", Messages: []importer.Message{{
		Role:    "assistant",
		Content: `MARSHAL_PROPOSAL {"action":"amend-deny"}`,
	}}}
	w.observeMarshalProposals(trDenial)
	batchDeny := w.permissions.queue.Take(false)
	if len(batchDeny) != 1 || batchDeny[0].Object != "/marshal amend deny" {
		t.Fatalf("unexpected queued permission: %+v", batchDeny)
	}
	if !strings.Contains(batchDeny[0].Reason, "Don't make this change to the plan: add new task") {
		t.Fatalf("unexpected reason: %s", batchDeny[0].Reason)
	}
	if err := w.decidePermission(t.Context(), batchDeny[0], false, "operator popup"); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	amendedStill := m.amended
	pendingStill := m.pending
	m.mu.Unlock()
	if !amendedStill || pendingStill == nil {
		t.Fatalf("amendment cleared despite owner denial: amended=%v pending=%+v", amendedStill, pendingStill)
	}

	// 2. Owner approval applies amend deny.
	tr := importer.SessionTranscript{SessionID: "chat", Messages: []importer.Message{{
		Role:    "assistant",
		Content: `MARSHAL_PROPOSAL {"action":"amend-deny"}`,
	}}}
	w.observeMarshalProposals(tr)
	batch := w.permissions.queue.Take(false)
	if len(batch) != 1 || batch[0].Object != "/marshal amend deny" {
		t.Fatalf("unexpected queued permission: %+v", batch)
	}
	if err := w.decidePermission(t.Context(), batch[0], true, "operator popup"); err != nil {
		t.Fatal(err)
	}

	m.mu.Lock()
	amended := m.amended
	pending := m.pending
	m.mu.Unlock()
	if amended || pending != nil {
		t.Fatalf("amendment was not cleared: amended=%v, pending=%+v", amended, pending)
	}
}
