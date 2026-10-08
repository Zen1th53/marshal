package tui

import (
	"context"
	"fmt"
	"github.com/Zen1th53/marshal/internal/memory/importer"
	"github.com/Zen1th53/marshal/internal/store"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/tmux"
)

func TestPermissionPopupRecordsBatchedDecisions(t *testing.T) {
	w, runtime := realControlWorkspace(t, "SESSION-popup-batch", false)
	dir := t.TempDir()
	fake := filepath.Join(dir, "tmux")
	// Execute the actual popup program with an operator-key fixture.
	script := "#!/bin/bash\nif [[ $* == *'#{client_height} #{client_width}'* ]]; then echo '40 120'; exit; fi\nif [[ $1 == list-clients ]]; then printf 'client|%%marshal|session\\n'; exit; fi\nif [[ $1 == display-message ]]; then printf '%%marshal\\n'; exit; fi\nif [[ $1 == display-popup ]]; then printf 'A' | bash -c \"${@: -1}\"; exit; fi\nexit 1\n"
	if err := os.WriteFile(fake, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	tmux.SetBinaryPath(fake)
	defer tmux.ResetBinaryPath()
	w.tmuxPath, w.tmuxSession = fake, "session"
	w.tmuxMarshalPaneID = "%marshal"
	w.startPermissionQueue()
	first, second := t.TempDir(), t.TempDir()
	req := permission.Request{Kind: "read", Object: first, Scope: "this session only, read-only", Who: "Marshal", Reason: "Continue earlier work"}
	w.queuePermission(req)
	w.queuePermission(req)
	req.Object = second
	w.queuePermission(req)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		w.permissions.mu.Lock()
		running := w.permissions.running
		w.permissions.mu.Unlock()
		if runtime.HasReadGrant(first) && runtime.HasReadGrant(second) && !running {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !runtime.HasReadGrant(first) || !runtime.HasReadGrant(second) {
		t.Fatal("popup did not grant the displayed objects")
	}
	events, err := runtime.Store().ListEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	decisions := 0
	for _, event := range events {
		if strings.Contains(event.Type, "PERMISSION_DECIDED") {
			decisions++
		}
	}
	if decisions != 2 {
		t.Fatalf("duplicate or missing decisions: %d", decisions)
	}
}

func TestLargeMemoryBatchRoutesToPerEntryReview(t *testing.T) {
	w, runtime := realControlWorkspace(t, "SESSION-overflow", false)
	ctx := context.Background()
	for i := 0; i < 88; i++ {
		_, _, err := runtime.ProposeContinuation(ctx, importer.SessionTranscript{SessionID: fmt.Sprintf("earlier-%d", i), Provider: "claude", CWD: runtime.ProjectRoot(), Messages: []importer.Message{{Role: "assistant", Content: fmt.Sprintf("Handoff %d", i)}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	var requests []permission.Request
	for _, rec := range runtime.ContinuationCandidates() {
		requests = append(requests, memoryPermission(rec))
	}
	requests = append(requests, permission.Request{Kind: "read", Object: t.TempDir()}, permission.Request{Kind: "read", Object: t.TempDir()})
	popup, review := partitionPermissionBatch(requests)
	if len(popup) != 2 || len(review) != 88 {
		t.Fatalf("popup=%d review=%d", len(popup), len(review))
	}
	// Drive the real queue: the read popup allows, while no memory decision is made.
	dir := t.TempDir()
	fake := filepath.Join(dir, "tmux")
	if err := os.WriteFile(fake, []byte("#!/bin/bash\nif [[ $* == *'#{client_height} #{client_width}'* ]]; then echo '40 120'; exit; fi\nif [[ $1 == list-clients ]]; then printf 'client|%%marshal|session\\n'; exit; fi\nif [[ $1 == display-message ]]; then printf '%%marshal\\n'; exit; fi\nif [[ $1 == display-popup ]]; then printf A | bash -c \"${@: -1}\"; exit; fi\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	tmux.SetBinaryPath(fake)
	defer tmux.ResetBinaryPath()
	w.tmuxPath, w.tmuxSession = fake, "session"
	w.tmuxMarshalPaneID = "%marshal"
	for _, req := range requests {
		w.permissions.queue.Add(req)
	}
	queueCtx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	w.runPermissionQueue(queueCtx)
	if !w.permissions.queue.Empty() {
		t.Fatal("permission queue did not drain")
	}
	records, err := runtime.Store().ListMemoryV2(ctx, store.MemoryQueryFilter{ProjectID: runtime.ProjectID()})
	if err != nil || len(records) != 0 || len(runtime.ContinuationCandidates()) != 88 {
		t.Fatalf("overflow wrote/lost candidates: %d %v", len(records), err)
	}
	events, err := runtime.Store().ListEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type == "PERMISSION_DECIDED" && event.Data["kind"] == "memory" {
			t.Fatal("overflow received blanket memory decision")
		}
	}
	for page := 1; page <= 18; page++ {
		text, err := w.cmd.handleMemoryReview(ctx, []string{"review", fmt.Sprint(page)})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(text, "MEM-IMPORT-") > permission.MaxPopupItems {
			t.Fatal("review page overflow")
		}
	}
	id := runtime.ContinuationCandidates()[0].ID
	if _, err := w.cmd.handleMemoryReview(ctx, []string{"allow", id}); err != nil {
		t.Fatal(err)
	}
	records, err = runtime.Store().ListMemoryV2(ctx, store.MemoryQueryFilter{ProjectID: runtime.ProjectID()})
	if err != nil || len(records) != 1 || records[0].ID != id {
		t.Fatalf("per-entry allow: %v %v", records, err)
	}
}

func TestPermissionQueueSmallTerminalDecidesOnlyVisibleRequest(t *testing.T) {
	w, runtime := realControlWorkspace(t, "SESSION-visible-pages", false)
	dir, err := os.MkdirTemp("", "uaD-popup-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	first, second := filepath.Join(dir, "first"), filepath.Join(dir, "second")
	for _, path := range []string{first, second} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	fake := filepath.Join(dir, "tmux")
	script := fmt.Sprintf(`#!/bin/bash
case $1 in
 list-clients) echo 'client|%%marshal|session';;
 display-message) case "${@: -1}" in *client_height*) echo '12 80';; *) echo 3.3a;; esac;;
 display-popup)
  if [[ -f %q ]]; then key=D; else key=A; fi
  echo popup >> %q
  printf "$key" | bash -c "${@: -1}" >> %q;;
 *) exit 1;;
esac
`, filepath.Join(dir, "calls"), filepath.Join(dir, "calls"), filepath.Join(dir, "screen"))
	if err := os.WriteFile(fake, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	tmux.SetBinaryPath(fake)
	defer tmux.ResetBinaryPath()
	w.tmuxPath, w.tmuxSession, w.tmuxMarshalPaneID = fake, "session", "%marshal"
	for _, path := range []string{first, second} {
		w.permissions.queue.Add(permission.Request{Kind: "read", Object: path})
	}
	w.runPermissionQueue(t.Context())
	if !runtime.HasReadGrant(first) || runtime.HasReadGrant(second) {
		t.Fatal("A/D did not apply separately to each visible request")
	}
	calls, _ := os.ReadFile(filepath.Join(dir, "calls"))
	if strings.Count(string(calls), "popup") != 2 {
		t.Fatalf("missing separate popups: %s", calls)
	}
	screen, _ := os.ReadFile(filepath.Join(dir, "screen"))
	if !strings.Contains(string(screen), first) || !strings.Contains(string(screen), second) {
		t.Fatalf("missing visible objects: %s", screen)
	}
}
