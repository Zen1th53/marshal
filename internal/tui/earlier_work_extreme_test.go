//go:build !governed

package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/permission"
)

func TestLaunchLoadsEarlierClaudeSessions(t *testing.T) {
	w, runtime := realControlWorkspace(t, "SESSION-earlier-work-load")
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("CODEX_HOME", t.TempDir())
	folder := runtime.EarlierWorkFolder("claude")
	if err := os.MkdirAll(folder, 0700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		data := fmt.Sprintf("{\"type\":\"user\",\"sessionId\":\"old-%d\",\"cwd\":%q,\"timestamp\":\"2026-11-10T10:00:00Z\",\"message\":{\"role\":\"user\",\"content\":\"Please retain the small function convention %d\"}}\n", i, runtime.ProjectRoot(), i)
		data += fmt.Sprintf("{\"type\":\"assistant\",\"sessionId\":\"old-%d\",\"cwd\":%q,\"timestamp\":\"2026-11-10T10:01:00Z\",\"message\":{\"role\":\"assistant\",\"content\":\"Keep functions small %d\"}}\n", i, runtime.ProjectRoot(), i)
		if err := os.WriteFile(filepath.Join(folder, fmt.Sprintf("old-%d.jsonl", i)), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}

	result := make(chan []scanSessions, 1)
	go func() { result <- w.scanEarlierSessions(context.Background(), runtime.ProjectRoot()) }()
	deadline := time.After(5 * time.Second)
	for {
		batch := w.permissions.queue.Take(false)
		if len(batch) != 0 {
			if len(batch) != 1 || batch[0].ContinuationProvider != "claude" {
				t.Fatalf("queued permissions = %+v", batch)
			}
			if !w.routeBuildPermission(context.Background(), batch[0]) {
				t.Fatal("shipped build did not decide the read automatically")
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("earlier-session read was not queued")
		case <-time.After(10 * time.Millisecond):
		}
	}
	var sessions []scanSessions
	select {
	case sessions = <-result:
	case <-time.After(5 * time.Second):
		t.Fatal("launch scan did not finish loading earlier sessions")
	}
	if len(sessions) != 1 || sessions[0].Provider != "claude" || sessions[0].Found != 2 || sessions[0].Loaded != 2 {
		t.Fatalf("sessions = %+v", sessions)
	}
	if got := (projectScan{Sessions: sessions}).briefingBlock(nil); !strings.Contains(got, "- earlier sessions: claude 2 loaded") {
		t.Fatalf("scan line missing: %s", got)
	}
	inbox, err := os.ReadFile(filepath.Join(runtime.ProjectRoot(), ".marshal", "inbox", "marshal.md"))
	if err != nil || strings.Count(string(inbox), "## runtime · granted continuation") != 1 {
		t.Fatalf("continuation inbox count=%d err=%v", strings.Count(string(inbox), "## runtime · granted continuation"), err)
	}
	w.mu.Lock()
	activity := w.state.LastOutput
	w.mu.Unlock()
	if !strings.Contains(activity, "Loaded 2 earlier Claude sessions for this project.") {
		t.Fatalf("loaded activity missing: %q", activity)
	}
	for _, req := range w.permissions.queue.Take(false) {
		if req.Kind == "read" && filepath.Clean(req.Object) == filepath.Clean(folder) {
			t.Fatalf("earlier-session read remained queued for a popup: %+v", req)
		}
	}
	events, err := runtime.Store().ListEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	decided := false
	for _, event := range events {
		object, _ := event.Data["object"].(string)
		actor, _ := event.Data["decided_by"].(string)
		if event.Type == "PERMISSION_DECIDED" && filepath.Clean(object) == filepath.Clean(folder) && actor == "marshal-runtime" {
			decided = true
		}
	}
	if !decided {
		t.Fatal("automatic read lacks PERMISSION_DECIDED evidence from marshal-runtime")
	}
}

func TestLaunchEarlierSessionsKeepOperatorDenial(t *testing.T) {
	w, runtime := realControlWorkspace(t, "SESSION-earlier-work-denied")
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("CODEX_HOME", t.TempDir())
	folder := runtime.EarlierWorkFolder("claude")
	if err := os.MkdirAll(folder, 0700); err != nil {
		t.Fatal(err)
	}
	data := fmt.Sprintf("{\"type\":\"user\",\"sessionId\":\"denied\",\"cwd\":%q,\"message\":{\"role\":\"user\",\"content\":\"denied earlier content\"}}\n", runtime.ProjectRoot())
	if err := os.WriteFile(filepath.Join(folder, "denied.jsonl"), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	deny := permission.Request{Kind: "read", Object: folder, Scope: "this project, until revoked, read-only", Who: "Marshal", Reason: "Load earlier claude work for this project"}
	if err := w.decidePermission(context.Background(), deny, false, "operator popup"); err != nil {
		t.Fatal(err)
	}
	if sessions := w.scanEarlierSessions(context.Background(), runtime.ProjectRoot()); len(sessions) != 0 {
		t.Fatalf("denied history was scanned: %+v", sessions)
	}
	if !w.permissions.queue.Empty() {
		t.Fatalf("denied history was queued for automatic allow: %+v", w.permissions.queue.Take(false))
	}
	if inbox, _ := os.ReadFile(filepath.Join(runtime.ProjectRoot(), ".marshal", "inbox", "marshal.md")); strings.Contains(string(inbox), "granted continuation") || strings.Contains(string(inbox), "denied earlier content") {
		t.Fatalf("earlier work delivered after an operator denial:\n%s", inbox)
	}
}

// Earlier work that cannot be loaded reads as such, not as a Marshal decision
// that "could not be applied".
func TestEarlierWorkFailedLineIsPlain(t *testing.T) {
	got := earlierWorkFailedLine("codex", errors.New("decode provider history: bufio.Scanner: token too long"))
	if !strings.HasPrefix(got, "Couldn't load earlier Codex work for this project (") || !strings.HasSuffix(got, "The run does not need it.") || strings.Contains(got, "decision") {
		t.Fatalf("line: %q", got)
	}
}
