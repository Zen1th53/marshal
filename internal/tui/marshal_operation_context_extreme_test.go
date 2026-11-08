//go:build !governed

package tui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/auth"
)

// This is the actual multiword command, owned Runtime, accepted project
// context and model launcher. The only executable named codex is our local
// synthetic CLI; its deliberate exit7 avoids inventing a model-authored plan.
func TestMarshalGoalKeepsActualOwnerContextForOwnedModelTurn(t *testing.T) {
	bin := t.TempDir()
	capture := filepath.Join(bin, "captured-prompt.json")
	script := "#!/usr/bin/python3 -I\nimport sys\nsys.dont_write_bytecode=True\nimport json,os\nif sys.argv[1:] == ['--version']:\n print('codex-cli 0.160.1'); sys.exit(0)\nif len(sys.argv) < 2 or sys.argv[1] != 'exec': sys.exit(98)\nfd=os.open(" + strconvQuoteForMarshalFixture(capture) + ",os.O_CREAT|os.O_EXCL|os.O_WRONLY,0o600)\nwith os.fdopen(fd,'w') as f: json.dump({'args':sys.argv[1:],'stdin':sys.stdin.read()},f)\nsys.exit(7)\n"
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	// The synthetic CLI only answers --version and exec, so the provider
	// sign-in probe has nothing to ask it; this test is about planning context.
	t.Cleanup(SetAuthPreflightForTest(func(context.Context, string, string, []string) error { return nil }))
	w, _, _, record, goal := nativePlanningFixture(t)
	m := w.marshalSession()
	m.mu.Lock()
	m.provider = "codex"
	m.mu.Unlock()
	command := "Preserve accepted planning policy"
	if _, err := (&CommandHandler{ws: w}).handleMarshal(t.Context(), strings.Fields(command)); err != nil {
		t.Fatal(err)
	}
	waitMarshalOperationStopped(t, w)
	raw, err := os.ReadFile(capture)
	if err != nil {
		t.Fatalf("owned model process was not reached: %v; panel=%+v", err, w.marshalPanel())
	}
	var captured struct {
		Args  []string `json:"args"`
		Stdin string   `json:"stdin"`
	}
	if err := json.Unmarshal(raw, &captured); err != nil || len(captured.Args) == 0 {
		t.Fatal("invalid synthetic command capture", err)
	}
	// The provider receives the prompt on standard input (the trailing "-"
	// argument), so the model-facing input is whatever the child read there.
	prompt := captured.Stdin
	if last := captured.Args[len(captured.Args)-1]; last != "-" {
		prompt = last
	}
	for _, required := range []string{command, record.Body, goal.DesiredOutcome, "Keep authoritative planning context complete"} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("actual provider input omitted accepted planning fact %q", required)
		}
	}
	panel := w.marshalPanel()
	if panel == nil || !strings.Contains(panel.Note, "exit status 7") {
		t.Fatalf("synthetic model failure was not surfaced: %+v", panel)
	}
}

func strconvQuoteForMarshalFixture(value string) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

func waitMarshalOperationStopped(t *testing.T, w *Workspace) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		m := w.marshalSession()
		m.mu.Lock()
		busy := m.busy
		m.mu.Unlock()
		if !busy {
			return
		}
		if time.Now().After(deadline) {
			w.marshalStop()
			t.Fatal("owned Marshal operation did not settle")
		}
		time.Sleep(time.Millisecond)
	}
}

// Missing Goal is a distinct bootstrap boundary. Supplying a local operator
// context alone must not turn it into a model call or create a synthetic Goal.
func TestMarshalGoalMissingCanonicalGoalCannotInvokeModel(t *testing.T) {
	bin := t.TempDir()
	capture := filepath.Join(bin, "called")
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/usr/bin/python3 -I\nimport sys\nsys.dont_write_bytecode=True\nimport pathlib\nif sys.argv[1:] == ['--version']:\n print('codex-cli 0.160.1'); sys.exit(0)\nif len(sys.argv) < 2 or sys.argv[1] != 'exec': sys.exit(98)\npathlib.Path("+strconvQuoteForMarshalFixture(capture)+").write_text('called')\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	w, _ := realControlWorkspace(t, "SESSION-missing-marshal-goal", false)
	w.marshalSession().provider = "codex"
	if _, err := (&CommandHandler{ws: w}).handleMarshal(context.Background(), []string{"Preserve", "planning", "policy"}); err != nil {
		t.Fatal(err)
	}
	waitMarshalOperationStopped(t, w)
	if _, err := os.Stat(capture); !os.IsNotExist(err) {
		t.Fatal("missing canonical Goal reached provider", err)
	}
	panel := w.marshalPanel()
	if panel == nil || !strings.Contains(panel.Note, "planning failed") {
		t.Fatalf("missing Goal refusal was not visible: %+v", panel)
	}
}

func TestMarshalOperationUsesOnlyActualWorkspaceOwner(t *testing.T) {
	w, _ := realControlWorkspace(t, "SESSION-operation-owner", false)
	ownerCtx, err := w.nativePlanningOwnerContext(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	owner, ok := auth.LocalFromContext(ownerCtx)
	if !ok {
		t.Fatal("fixture lacks trusted owner")
	}
	userCtx, stopUser := context.WithCancel(context.WithValue(ownerCtx, marshalOperationFixtureKey{}, "user-context"))
	stopUser()
	reserved, cancel, err := w.reserveMarshalOperation(userCtx)
	if err != nil {
		t.Fatal(err)
	}
	m := w.marshalSession()
	defer m.finish(cancel)
	actual, ok := auth.LocalFromContext(reserved)
	if !ok || actual.ID() != owner.ID() || actual.ProjectID() != w.runtime.ProjectIdentity() || reserved.Value(marshalOperationFixtureKey{}) != "user-context" || reserved.Err() != nil {
		t.Fatal("reserved operation lost actual owner/user context or inherited user cancellation")
	}
	m.finish(cancel)

	// Even a caller carrying a real foreign LocalControl context cannot confer
	// workspace ownership on an unattached Workspace or a substituted authority.
	unattached := NewWorkspace(nil, "foreign-project", "foreign-session")
	if _, _, err := unattached.reserveMarshalOperation(ownerCtx); err == nil {
		t.Fatal("caller context authorized an unattached workspace")
	}
	foreign, _ := realControlWorkspace(t, "SESSION-foreign-operation-owner", false)
	foreignControl := foreign.controlSource()
	w.mu.Lock()
	w.controlOverride = foreignControl
	w.mu.Unlock()
	if _, _, err := w.reserveMarshalOperation(ownerCtx); err == nil {
		t.Fatal("substituted foreign runtime authority accepted the real owner label")
	}
	m.mu.Lock()
	busy := m.busy
	m.mu.Unlock()
	if busy {
		t.Fatal("foreign owner refusal acquired an operation reservation")
	}
}
