//go:build governed

package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLaunchEarlierSessionsNeedGrant(t *testing.T) {
	w, runtime := realControlWorkspace(t, "SESSION-earlier-work-grant")
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("CODEX_HOME", t.TempDir())
	folder := runtime.EarlierWorkFolder("claude")
	if err := os.MkdirAll(folder, 0700); err != nil {
		t.Fatal(err)
	}
	transcript := `{"type":"user","sessionId":"private-earlier","cwd":"` + runtime.ProjectRoot() + `","message":{"role":"user","content":"private earlier content"}}` + "\n"
	if err := os.WriteFile(filepath.Join(folder, "earlier.jsonl"), []byte(transcript), 0600); err != nil {
		t.Fatal(err)
	}
	sessions := w.scanEarlierSessions(context.Background(), runtime.ProjectRoot())
	if len(sessions) != 1 || !sessions[0].NeedsGrant || sessions[0].Found != 0 || sessions[0].Loaded != 0 {
		t.Fatalf("sessions = %+v", sessions)
	}
	if line := (projectScan{Sessions: sessions}).briefingBlock(nil); !strings.Contains(line, "claude needs your OK") {
		t.Fatalf("grant notice missing: %s", line)
	}
	if runtime.HasReadGrant(folder) {
		t.Fatal("earlier history was granted before operator approval")
	}
	batch := w.permissions.queue.Take(false)
	if len(batch) != 1 || batch[0].Kind != "read" || batch[0].ContinuationProvider != "claude" {
		t.Fatalf("queued read popup = %+v", batch)
	}
	if _, err := os.Stat(filepath.Join(runtime.ProjectRoot(), ".marshal", "inbox", "marshal.md")); !os.IsNotExist(err) {
		t.Fatalf("inbox was written before grant: %v", err)
	}
	w.launch.mu.Lock()
	w.launch.scan = projectScan{Sessions: sessions}
	w.launch.mu.Unlock()
	if !w.earlierWorkFolderFound(folder) {
		t.Fatal("this project's existing history folder is not eligible for its grant popup")
	}

	// The operator allows the popup: the ordinary decision path loads it.
	if err := w.decidePermission(context.Background(), batch[0], true, "operator popup"); err != nil {
		t.Fatal(err)
	}
	inbox, err := os.ReadFile(filepath.Join(runtime.ProjectRoot(), ".marshal", "inbox", "marshal.md"))
	if err != nil || !strings.Contains(string(inbox), "## runtime · granted continuation") || !strings.Contains(string(inbox), "press A to apply") {
		t.Fatalf("allowed read did not deliver earlier work: %v\n%s", err, inbox)
	}

	// The next launch counts and loads under the stored grant without asking again.
	again := w.scanEarlierSessions(context.Background(), runtime.ProjectRoot())
	if len(again) != 1 || again[0].NeedsGrant || again[0].Found != 1 || again[0].Loaded != 1 {
		t.Fatalf("granted sessions = %+v", again)
	}
	// Memory candidates from the load still wait for their own reviews.
	for _, req := range w.permissions.queue.Take(false) {
		if req.Kind == "read" {
			t.Fatalf("granted history asked again: %+v", req)
		}
	}
}
