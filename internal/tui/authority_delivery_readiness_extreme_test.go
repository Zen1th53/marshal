//go:build !governed

package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/permission"
)

func codexIdleFixture(t *testing.T, root, session, suffix string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rollout-"+session+".jsonl")
	meta, _ := json.Marshal(map[string]any{"type": "session_meta", "payload": map[string]string{"id": session, "cwd": root, "cli_version": "0.162.1"}})
	data := string(meta) + "\n" + `{"type":"event_msg","payload":{"type":"task_started","turn_id":"owned-turn"}}` + "\n" + suffix
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

const codexCompleteEvent = `{"type":"event_msg","payload":{"type":"task_complete","turn_id":"owned-turn"}}` + "\n"

func TestCodexTranscriptReadinessRequiresBoundRegularCurrentIdleSession(t *testing.T) {
	root := t.TempDir()
	session := "owned-session"
	cases := []struct {
		name, suffix string
		ready        bool
	}{
		{"idle", codexCompleteEvent, true},
		{"active", "", false},
		{"foreign-completion", `{"type":"event_msg","payload":{"type":"task_complete","turn_id":"foreign"}}` + "\n", false},
		{"new-turn", codexCompleteEvent + `{"type":"event_msg","payload":{"type":"task_started","turn_id":"new"}}` + "\n", false},
		{"new-tool", codexCompleteEvent + `{"type":"response_item","payload":{"type":"function_call"}}` + "\n", false},
		{"unknown-event", codexCompleteEvent + `{"type":"event_msg","payload":{"type":"unknown-protocol"}}` + "\n", false},
		{"accounting", codexCompleteEvent + `{"type":"event_msg","payload":{"type":"token_count"}}` + "\n", true},
		{"torn", codexCompleteEvent + `{"type":"event_msg"`, false},
		{"malformed", codexCompleteEvent + "invalid\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := codexIdleFixture(t, root, session, tc.suffix)
			if got := codexTranscriptIdle(path, session, root); got != tc.ready {
				t.Fatalf("ready=%v want%v", got, tc.ready)
			}
			if codexTranscriptIdle(path, "foreign", root) || codexTranscriptIdle(path, session, t.TempDir()) {
				t.Fatal("foreign metadata admitted")
			}
		})
	}
	path := codexIdleFixture(t, root, session, codexCompleteEvent)
	alias := filepath.Join(t.TempDir(), "alias.jsonl")
	if err := os.Symlink(path, alias); err != nil {
		t.Fatal(err)
	}
	if codexTranscriptIdle(alias, session, root) {
		t.Fatal("symlink file admitted")
	}
	dirAlias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(filepath.Dir(path), dirAlias); err != nil {
		t.Fatal(err)
	}
	if codexTranscriptIdle(filepath.Join(dirAlias, filepath.Base(path)), session, root) {
		t.Fatal("symlink parent admitted")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), "0.162.1", "unknown", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	if codexTranscriptIdle(path, session, root) {
		t.Fatal("unknown protocol admitted")
	}
	if codexTranscriptIdle(filepath.Join(t.TempDir(), "missing"), session, root) {
		t.Fatal("missing resumed history admitted")
	}
}
func TestCodexComposerReadinessRejectsModalModeAndOtherPayload(t *testing.T) {
	frame := "› \n\n  GPT-6.1-Sol default · /owned\n"
	status := "%42\t2\t0\t3\t0"
	if !codexComposerReady(frame, status, "%42", "") {
		t.Fatal("clean composer rejected")
	}
	for _, bad := range []string{"%43\t2\t0\t3\t0", "%42\t2\t0\t3\t1", "%42\t1\t0\t3\t0", "%42\t2\t20\t3\t0", "unknown"} {
		if codexComposerReady(frame, bad, "%42", "") {
			t.Fatalf("bad status admitted: %s", bad)
		}
	}
	payload := "Own request exact decision-id"
	if !codexComposerReady("› "+payload+"\n\n  GPT-6.1-Sol default · /owned\n", status, "%42", payload) {
		t.Fatal("own payload rejected")
	}
	for _, draft := range []string{"foreign draft", payload + " altered", fmt.Sprintf("[Pasted Content %d chars]", len(payload)+1)} {
		if codexComposerReady("› "+draft+"\n\n  GPT-6.1-Sol default · /owned\n", status, "%42", payload) {
			t.Fatal("foreign payload admitted")
		}
	}
	if codexComposerReady("› \n enter to submit | esc to cancel\n GPT-6.1-Sol default · /owned\n", status, "%42", "") {
		t.Fatal("tool modal admitted")
	}
}
func TestCodexDecisionBoundTranscriptDefersUntilActualMatchingTurnCompletes(t *testing.T) {
	marshalDecidesWork(t)
	w := startupCodexDecisionWorkspace(t)
	log, _ := fakeCodexDecisionPane(t, "pasted")
	path := codexIdleFixture(t, w.runtime.ProjectRoot(), "bound-session", "")
	w.tmuxMu.Lock()
	a := w.tmuxActiveWins["marshal-chat"]
	a.sessionID = "bound-session"
	a.historyPath = path
	w.tmuxMu.Unlock()
	req := permission.Request{Kind: "read", Object: t.TempDir()}
	w.routeBuildPermission(t.Context(), req)
	id, p := pendingCodexDecision(t, w)
	if len(decisionCalls(t, log)) != 0 || p.delivered {
		t.Fatal("active transcript allowed input")
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString(codexCompleteEvent); err != nil {
		t.Fatal(err)
	}
	f.Close()
	w.routeBuildPermission(t.Context(), req)
	id2, p2 := pendingCodexDecision(t, w)
	if id2 != id || p2 != p || !p.delivered {
		t.Fatal("completed same turn did not deliver original request")
	}
}
func TestCodexUndeliveredQuestionExpiryIsAuditedAndNeverInjected(t *testing.T) {
	marshalDecidesWork(t)
	w := startupCodexDecisionWorkspace(t)
	log, mode := fakeCodexDecisionPane(t, "pasted")
	os.WriteFile(mode, []byte("modal"), 0600)
	req := permission.Request{Kind: "read", Object: t.TempDir()}
	w.routeBuildPermission(t.Context(), req)
	_, p := pendingCodexDecision(t, w)
	w.permissions.mu.Lock()
	p.deadline = time.Now().Add(-time.Second)
	w.permissions.mu.Unlock()
	w.routeBuildPermission(t.Context(), req)
	for _, call := range decisionCalls(t, log) {
		if call.Args[0] != "display-message" && call.Args[0] != "capture-pane" {
			t.Fatal("expired deferred question injected")
		}
	}
	w.permissions.mu.Lock()
	pending := len(w.permissions.build.pending)
	w.permissions.mu.Unlock()
	if pending != 0 {
		t.Fatal("expired request retained")
	}
	events, err := w.store.ListEvents(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// Extreme never denies an unanswered question: it is re-delivered with
	// backoff, durably recorded, and never granted without an answer.
	found := false
	for _, event := range events {
		if event.Type == "PERMISSION_DECIDED" && event.Data["object"] == req.Object {
			t.Fatalf("unanswered question decided: %+v", event.Data)
		}
		if event.Type == "PERMISSION_REDELIVERY_SCHEDULED" && event.Data["object"] == req.Object {
			found = true
		}
	}
	if !found {
		t.Fatal("expiry re-delivery not durable")
	}
}

func TestCodexSecondQuestionWaitsForFirstPendingDecision(t *testing.T) {
	marshalDecidesWork(t)
	w := startupCodexDecisionWorkspace(t)
	log, _ := fakeCodexDecisionPane(t, "pasted")
	first := permission.Request{Kind: "read", Object: t.TempDir()}
	second := permission.Request{Kind: "read", Object: t.TempDir()}
	w.routeBuildPermission(t.Context(), first)
	firstID, firstPending := pendingCodexDecision(t, w)
	w.routeBuildPermission(t.Context(), second)
	w.permissions.mu.Lock()
	var secondID string
	var secondPending *buildPendingDecision
	for id, p := range w.permissions.build.pending {
		if p.request == second {
			secondID, secondPending = id, p
		}
	}
	w.permissions.mu.Unlock()
	if secondPending == nil {
		t.Fatal("second original pending question missing")
	}
	deadline := secondPending.deadline
	for i := 0; i < 4; i++ {
		w.routeBuildPermission(t.Context(), second)
	}
	w.permissions.mu.Lock()
	same := w.permissions.build.pending[firstID] == firstPending && w.permissions.build.pending[secondID] == secondPending && secondPending.deadline == deadline && !secondPending.delivered
	w.permissions.mu.Unlock()
	if !same {
		t.Fatal("second question lost binding or bypassed first decision")
	}
	counts := map[string]int{}
	for _, call := range decisionCalls(t, log) {
		counts[call.Args[0]]++
	}
	if counts["paste-buffer"] != 1 || counts["send-keys"] != 1 {
		t.Fatalf("multiple questions injected into one active composer: %+v", counts)
	}
	if explicitReadGrant(w.runtime, first.Object) || explicitReadGrant(w.runtime, second.Object) {
		t.Fatal("question delivery granted authority")
	}
}

func TestCodexTranscriptTailWithoutMatchingStartRemainsUnknown(t *testing.T) {
	root := t.TempDir()
	path := codexIdleFixture(t, root, "owned-session", "")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	item, _ := json.Marshal(map[string]any{"type": "response_item", "payload": map[string]string{"type": "function_call_output", "output": strings.Repeat("x", 2<<20)}})
	if _, err = f.Write(append(item, '\n')); err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString(codexCompleteEvent); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if codexTranscriptIdle(path, "owned-session", root) {
		t.Fatal("bounded tail fabricated omitted start observation")
	}
}

func TestCodexTranscriptTruncatedPrefixRequiresLaterCompleteKnownTurn(t *testing.T) {
	start := func(id string) string {
		return fmt.Sprintf("{\"type\":\"event_msg\",\"payload\":{\"type\":\"task_started\",\"turn_id\":%q}}\n", id)
	}
	complete := func(id string) string {
		return fmt.Sprintf("{\"type\":\"event_msg\",\"payload\":{\"type\":\"task_complete\",\"turn_id\":%q}}\n", id)
	}
	cases := []struct {
		name, suffix string
		ready        bool
	}{
		{"fresh-pair", complete("old") + start("fresh") + complete("fresh"), true},
		{"fresh-active", complete("old") + start("fresh"), false},
		{"missing-start", complete("old") + complete("fresh"), false},
		{"known-mismatch", complete("old") + start("fresh") + complete("other") + start("later") + complete("later"), false},
		{"empty-prefix-completion", complete("") + start("fresh") + complete("fresh"), false},
		{"empty-start", complete("old") + start("") + start("fresh") + complete("fresh"), false},
		{"empty-completion", complete("old") + start("fresh") + complete(""), false},
		{"torn", complete("old") + start("fresh") + strings.TrimSuffix(complete("fresh"), "\n"), false},
		{"malformed", complete("old") + "invalid\n" + start("fresh") + complete("fresh"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := codexIdleFixture(t, root, "owned-session", "")
			// Force the bounded read to begin inside a valid old output line,
			// leaving its completion as the first in-bound turn marker.
			item, _ := json.Marshal(map[string]any{"type": "response_item", "payload": map[string]string{"type": "function_call_output", "output": strings.Repeat("x", 2<<20)}})
			f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0600)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.Write(append(item, '\n')); err != nil {
				t.Fatal(err)
			}
			if _, err = f.WriteString(tc.suffix); err != nil {
				t.Fatal(err)
			}
			if err = f.Close(); err != nil {
				t.Fatal(err)
			}
			if got := codexTranscriptIdle(path, "owned-session", root); got != tc.ready {
				t.Fatalf("ready=%v want=%v", got, tc.ready)
			}
		})
	}
	root := t.TempDir()
	path := codexIdleFixture(t, root, "owned-session", "")
	meta, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	meta = meta[:strings.IndexByte(string(meta), '\n')+1]
	if err = os.WriteFile(path, append(meta, []byte(complete("old")+start("fresh")+complete("fresh"))...), 0600); err != nil {
		t.Fatal(err)
	}
	if codexTranscriptIdle(path, "owned-session", root) {
		t.Fatal("untruncated orphan completion admitted")
	}
}

func TestDecisionJSONEscapesEveryTerminalControlWithoutChangingData(t *testing.T) {
	reason := "\n\x1b\x7f\u0085\u202e\U000e0001"
	data, _ := json.Marshal(map[string]string{"reason": reason})
	safe := terminalSafeDecisionJSON(data)
	if strings.ContainsAny(safe, reason) {
		t.Fatal("terminal control remained literal")
	}
	var decoded map[string]string
	if err := json.Unmarshal([]byte(safe), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["reason"] != reason {
		t.Fatal("escaping changed decision description data")
	}
}

func TestCodexNativeTurnStartingAfterPasteCannotReceiveEnter(t *testing.T) {
	marshalDecidesWork(t)
	w := startupCodexDecisionWorkspace(t)
	log, _ := fakeCodexDecisionPane(t, "pasted")
	path := codexIdleFixture(t, w.runtime.ProjectRoot(), "bound-session", codexCompleteEvent)
	w.tmuxMu.Lock()
	a := w.tmuxActiveWins["marshal-chat"]
	a.sessionID = "bound-session"
	a.historyPath = path
	w.tmuxMu.Unlock()
	req := permission.Request{Kind: "read", Object: t.TempDir()}
	done := make(chan bool, 1)
	go func() { done <- w.routeBuildPermission(t.Context(), req) }()
	until := time.Now().Add(3 * time.Second)
	for {
		found := false
		for _, call := range decisionCalls(t, log) {
			found = found || call.Args[0] == "paste-buffer"
		}
		if found {
			break
		}
		if time.Now().After(until) {
			t.Fatal("no owned paste")
		}
		time.Sleep(time.Millisecond)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString(`{"type":"event_msg","payload":{"type":"task_started","turn_id":"new-actual-turn"}}` + "\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if !<-done {
		t.Fatal("not routed")
	}
	for _, call := range decisionCalls(t, log) {
		if call.Args[0] == "send-keys" {
			t.Fatal("active native turn received Enter despite unchanged composer")
		}
	}
	if explicitReadGrant(w.runtime, req.Object) {
		t.Fatal("active turn failure granted authority")
	}
	// Extreme re-delivers a failed delivery, but only after its backoff: it is
	// never respawned at once as a fresh question.
	w.permissions.mu.Lock()
	r := w.permissions.build.redeliver[req.Key()]
	pending := len(w.permissions.build.pending)
	w.permissions.mu.Unlock()
	if pending != 0 || r == nil || !r.next.After(time.Now()) {
		t.Fatal("failed delivery respawned as a fresh question")
	}
}

func TestCodexStyledEmptyPlaceholderIsDistinctFromOperatorDraft(t *testing.T) {
	const placeholder = "\x1b[0;1m›\x1b[0m \x1b[2mAsk Codex to do anything\x1b[0m"
	const resumedPlaceholder = "\x1b[1m›\x1b[0m \x1b[2mAsk Codex to do anything\x1b[0m"
	const footer = "\n\n\x1b[2m  GPT-6.1-Sol default · /owned\x1b[0m\n"
	status := "%42\t2\t0\t3\t0"
	for _, observed := range []string{placeholder, resumedPlaceholder} {
		if !codexComposerReady(observed+footer, status, "%42", "") {
			t.Fatal("grounded empty styled composer rejected")
		}
		if codexComposerReady(observed+footer, "%42\t0\t0\t3\t0", "%42", "") {
			t.Fatal("Home cursor before typed samephrase admitted")
		}
	}
	for _, draft := range []string{"› Ask Codex to do anything", "\x1b[0;1m›\x1b[0m Ask Codex to do anything", strings.Replace(placeholder, "[2m", "[1m", 1), strings.Replace(resumedPlaceholder, "[1m", "[3m", 1), strings.Replace(resumedPlaceholder, "[2m", "[22m", 1), placeholder + " extra", resumedPlaceholder + " extra"} {
		if codexComposerReady(draft+footer, status, "%42", "") {
			t.Fatal("plain/operator/unknownstyled draft admitted as placeholder")
		}
	}
	if codexComposerReady(placeholder+footer, "%42\t3\t0\t3\t0", "%42", "") {
		t.Fatal("cursor inside placeholder admitted")
	}
	if codexComposerReady(placeholder+"\n enter to submit | esc to cancel\n GPT-6.1-Sol default · /owned\n", status, "%42", "") {
		t.Fatal("placeholder with consent modal admitted")
	}
	if codexComposerReady(placeholder+footer, status, "%42", "own request") {
		t.Fatal("empty placeholder admitted as own pasted request")
	}
}
