package tui

import (
	"context"
	"fmt"
	"github.com/Zen1th53/marshal/internal/memory/importer"
	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/tmux"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAcceptanceOwnChatWithoutHistoryGrant(t *testing.T) {
	w, rt := realControlWorkspace(t, "SESSION-own-chat")
	root, dir := rt.ProjectRoot(), t.TempDir()
	old := filepath.Join(dir, "earlier.jsonl")
	if err := os.WriteFile(old, []byte("invalid earlier history must never be decoded\n"), 0600); err != nil {
		t.Fatal(err)
	}
	watch := newNativeHistoryWatch(dir, root)
	w.guardHistoryWatch(watch, "codex")
	watch.consume = func(importer.SessionTranscript) error {
		return fmt.Errorf("history importer ran without its read grant")
	}
	if !w.permissions.queue.Empty() {
		t.Fatal("unsolicited startup history request")
	}
	w.tmuxActiveWins = map[string]*activeTmuxAgent{"marshal-chat": {id: "marshal-chat", provider: "codex", role: "marshal-chat"}}
	if _, err := w.prepareChatHistoryWatch(root, watch, ""); err != nil {
		t.Fatal(err)
	}
	data := fmt.Sprintf("{\"type\":\"session_meta\",\"payload\":{\"id\":\"chat\",\"cwd\":%q}}\n{\"type\":\"response_item\",\"payload\":{\"type\":\"message\",\"role\":\"assistant\",\"phase\":\"final_answer\",\"content\":[{\"type\":\"output_text\",\"text\":%q}]}}\n", root, `MARSHAL_PROPOSAL {"action":"setting","key":"control","value":"strict"}`)
	if err := os.WriteFile(filepath.Join(dir, "current.jsonl"), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	if err := watch.sync(); err != nil {
		t.Fatal(err)
	}
	batch := w.permissions.queue.Take(false)
	if len(batch) != 1 || batch[0].Object != "/marshal settings control strict" {
		t.Fatalf("proposal missing: %v", batch)
	}
	if loadChatBinding(root).SessionID != "chat" {
		t.Fatal("resume identity missing")
	}
	if rt.HasReadGrant(dir) {
		t.Fatal("own observation granted earlier history")
	}
	// Reopen with a durable identity and a filename independent of that ID.
	saved := loadChatBinding(root)
	if saved.HistoryPath != filepath.Join(dir, "current.jsonl") {
		t.Fatal("current transcript path not retained")
	}
	resumed := newNativeHistoryWatch(dir, root)
	w.guardHistoryWatch(resumed, "codex")
	if _, err := w.prepareChatHistoryWatch(root, resumed, saved.SessionID); err != nil {
		t.Fatal(err)
	}
	if err := resumed.sync(); err != nil {
		t.Fatal(err)
	}
	if !resumed.chatFile(saved.HistoryPath) || resumed.chatFile(old) {
		t.Fatal("reopened chat lost exact transcript binding")
	}
}

func TestAcceptanceIntakeSurvivesProviderSwitch(t *testing.T) {
	w, rt := realControlWorkspace(t, "SESSION-intake")
	emitMarshalIntakeFile(t, w, "Uzbek", "no")
	fresh := NewWorkspace(nil, "project", "session")
	brief := fresh.marshalContinuityBriefing(rt.ProjectRoot(), "protocol")
	if !strings.Contains(brief, "Uzbek") || !strings.Contains(brief, `"earlier_work":"no"`) || !strings.Contains(brief, "Do not repeat") {
		t.Fatalf("intake lost: %s", brief)
	}
	if !w.permissions.queue.Empty() {
		t.Fatal("intake requested permission")
	}
}

func TestAcceptancePermissionTargetsClientAndDefersHistory(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "tmux")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nprintf '/dev/pts/7|%%chat|session\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	tmux.SetBinaryPath(fake)
	defer tmux.ResetBinaryPath()
	w := NewWorkspace(nil, "project", "session")
	w.tmuxSession = "session"
	w.tmuxMarshalPaneID = "%centre"
	w.tmuxActiveWins = map[string]*activeTmuxAgent{"marshal-chat": {role: "marshal-chat", paneID: "%chat"}}
	target := w.permissionPopupTarget(context.Background(), []permission.Request{{Kind: "marshal-command"}})
	if target != "/dev/pts/7" {
		t.Fatalf("popup target=%q", target)
	}
	w.tmuxActiveWins = nil
	if got := w.permissionPopupTarget(context.Background(), []permission.Request{{Kind: "marshal-command"}}); got != "" {
		t.Fatal("proposal interrupts an unregistered provider pane")
	}
	w.tmuxActiveWins = map[string]*activeTmuxAgent{"marshal-chat": {role: "marshal-chat", paneID: "%chat"}}
	if got := w.permissionPopupTarget(context.Background(), []permission.Request{{Kind: "read"}}); got != "" {
		t.Fatalf("history interrupts chat: %s", got)
	}
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nprintf '/dev/pts/7|%%centre|session\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if got := w.permissionPopupTarget(context.Background(), []permission.Request{{Kind: "read"}}); got != "/dev/pts/7" {
		t.Fatalf("history unavailable at centre: %s", got)
	}
}

func TestAcceptanceHistoryProposalRequiresEarlierWorkYes(t *testing.T) {
	w, rt := realControlWorkspace(t, "SESSION-history-intake")
	proposal := importer.SessionTranscript{SessionID: "chat", Messages: []importer.Message{{Role: "assistant", Content: `MARSHAL_PROPOSAL {"action":"read","path":"/tmp/earlier"}`}}}
	w.observeMarshalProposals(proposal)
	if !w.permissions.queue.Empty() {
		t.Fatal("history request before earlier-work answer")
	}
	emitMarshalIntakeFile(t, w, "Uzbek", "no")
	w.observeMarshalProposals(proposal)
	if !w.permissions.queue.Empty() {
		t.Fatal("history request after no")
	}
	emitMarshalIntakeFile(t, w, "Uzbek", "yes")
	w.observeMarshalProposals(proposal)
	batch := w.permissions.queue.Take(false)
	if len(batch) != 1 || batch[0].Kind != "read" {
		t.Fatalf("yes did not enable explicit history request: %v", batch)
	}
	if rt.HasReadGrant("/tmp/earlier") {
		t.Fatal("intake yes granted history without popup")
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "tmux")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nprintf '/dev/pts/7|%%chat|session\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	tmux.SetBinaryPath(fake)
	defer tmux.ResetBinaryPath()
	w.tmuxSession = "session"
	w.tmuxMarshalPaneID = "%centre"
	w.tmuxActiveWins = map[string]*activeTmuxAgent{"marshal-chat": {paneID: "%chat", role: "marshal-chat"}}
	if got := w.permissionPopupTarget(t.Context(), batch); got != "/dev/pts/7" {
		t.Fatalf("consented history popup not visible in Marshal chat: %s", got)
	}
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nprintf '/dev/pts/7|%%native|session\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if got := w.permissionPopupTarget(t.Context(), batch); got != "" {
		t.Fatalf("history popup would swallow native keys: %s", got)
	}
}

func TestAcceptanceProposalPopupAppliesOnChatClient(t *testing.T) {
	w, rt := realControlWorkspace(t, "SESSION-client-popup")
	dir := t.TempDir()
	fake := filepath.Join(dir, "tmux")
	log := filepath.Join(dir, "target")
	script := fmt.Sprintf(`#!/bin/bash
if [[ $* == *'#{client_height} #{client_width}'* ]]; then echo '40 120'; exit; fi
case "$1" in
 list-clients) printf '/dev/pts/7|%%%%chat|session\n';;
 display-message) echo 3.3a;;
 display-popup) printf '%%s\n' "$@" > %q; printf A | bash -c "${@: -1}";;
 *) exit 1;;
esac
`, log)
	if err := os.WriteFile(fake, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	tmux.SetBinaryPath(fake)
	defer tmux.ResetBinaryPath()
	w.tmuxSession = "session"
	w.tmuxPath = fake
	w.tmuxMarshalPaneID = "%centre"
	w.tmuxActiveWins = map[string]*activeTmuxAgent{"marshal-chat": {role: "marshal-chat", paneID: "%chat"}}
	w.observeMarshalProposals(importer.SessionTranscript{SessionID: "chat", Messages: []importer.Message{{Role: "assistant", Content: `MARSHAL_PROPOSAL {"action":"setting","key":"control","value":"strict"}`}}})
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { w.runPermissionQueue(ctx); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("popup queue did not finish")
	}
	settings, err := rt.Marshal().Store.GetMarshalSettings(t.Context(), rt.ProjectID())
	if err != nil {
		t.Fatal(err)
	}
	if string(settings.Value.Control) != "strict" {
		t.Fatal("A in chat popup did not apply proposal")
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "-t\n/dev/pts/7\n") {
		t.Fatalf("popup did not target attached client: %s", data)
	}
}
