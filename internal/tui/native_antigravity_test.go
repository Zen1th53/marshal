package tui

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// agy records the workspace a conversation ran in as a JSON array of file
// URIs. Reading it any other way attributes every conversation to no project,
// which silently imports nothing.
func TestAntigravityWorkspaceMatchesTheRecordedFormat(t *testing.T) {
	root := t.TempDir()
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	watch := newNativeHistoryWatch("", resolved)

	for name, tc := range map[string]struct {
		column string
		want   bool
	}{
		"json array":          {`["file://` + resolved + `"]`, true},
		"json array, several": {`["file:///elsewhere","file://` + resolved + `"]`, true},
		"another project":     {`["file:///elsewhere"]`, false},
		"plain path":          {resolved, true},
		"comma separated":     {"file:///elsewhere,file://" + resolved, true},
	} {
		uris := splitWorkspaceURIs(tc.column)
		if got := watch.antigravityWorkspaceMatches(uris); got != tc.want {
			t.Errorf("%s: %q matched=%v, want %v (parsed %q)", name, tc.column, got, tc.want, uris)
		}
	}
	if uris := splitWorkspaceURIs(""); len(uris) != 0 {
		t.Fatalf("an empty column should record no workspace, got %q", uris)
	}
}

func TestAntigravityDirectLaunchCapturesNewConversationWithoutSummaries(t *testing.T) {
	dir := t.TempDir()
	root := t.TempDir()
	watch := newNativeHistoryWatch(dir, root)
	watch.antigravity = true

	if err := watch.prepareAntigravityDirectLaunch(nil); err != nil {
		t.Fatal(err)
	}
	if watch.chatFile == nil {
		t.Fatal("expected chatFile to be set after prepareAntigravityDirectLaunch")
	}

	launched := filepath.Join(dir, "conv-new.db")
	if err := os.WriteFile(launched, []byte("sqlite-data"), 0o600); err != nil {
		t.Fatal(err)
	}

	if !watch.chatFile(launched) {
		t.Fatalf("expected chatFile to accept newly created conversation %s", launched)
	}
}

func TestAntigravityDirectLaunchRejectsExistingConversation(t *testing.T) {
	dir := t.TempDir()
	root := t.TempDir()

	old := filepath.Join(dir, "conv-old.db")
	if err := os.WriteFile(old, []byte("sqlite-data"), 0o600); err != nil {
		t.Fatal(err)
	}

	watch := newNativeHistoryWatch(dir, root)
	watch.antigravity = true

	if err := watch.prepareAntigravityDirectLaunch(nil); err != nil {
		t.Fatal(err)
	}

	newConv := filepath.Join(dir, "conv-new.db")
	if err := os.WriteFile(newConv, []byte("sqlite-data"), 0o600); err != nil {
		t.Fatal(err)
	}

	if watch.chatFile(old) {
		t.Fatalf("expected chatFile to reject conversation existing before launch: %s", old)
	}
	if !watch.chatFile(newConv) {
		t.Fatalf("expected chatFile to accept new conversation: %s", newConv)
	}
}

func TestAntigravityDirectLaunchMatchesExplicitConversationID(t *testing.T) {
	dir := t.TempDir()
	root := t.TempDir()
	watch := newNativeHistoryWatch(dir, root)
	watch.antigravity = true

	if err := watch.prepareAntigravityDirectLaunch([]string{"--conversation", "explicit-123"}); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(dir, "explicit-123.db")
	other := filepath.Join(dir, "other-456.db")

	if !watch.chatFile(target) {
		t.Fatalf("expected chatFile to match explicit target conversation %s", target)
	}
	if watch.chatFile(other) {
		t.Fatalf("expected chatFile to reject non-target conversation %s", other)
	}
}

func TestAntigravityDirectLaunchPicksNewestCandidate(t *testing.T) {
	dir := t.TempDir()
	root := t.TempDir()
	watch := newNativeHistoryWatch(dir, root)
	watch.antigravity = true

	if err := watch.prepareAntigravityDirectLaunch(nil); err != nil {
		t.Fatal(err)
	}

	early := filepath.Join(dir, "conv-early.db")
	if err := os.WriteFile(early, []byte("sqlite-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-5 * time.Second)
	if err := os.Chtimes(early, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	late := filepath.Join(dir, "conv-late.db")
	if err := os.WriteFile(late, []byte("sqlite-data"), 0o600); err != nil {
		t.Fatal(err)
	}

	if watch.chatFile(early) {
		t.Fatalf("expected chatFile to reject older concurrent candidate %s", early)
	}
	if !watch.chatFile(late) {
		t.Fatalf("expected chatFile to bind newest candidate %s", late)
	}
}
