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

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/testutil/testgit"
)

func runtimeRecoveryFixture(t *testing.T) (*Workspace, *proposalMCPLease) {
	t.Helper()
	repo := testgit.New(t)
	for _, name := range []string{"CAPABILITIES.yaml", "PACK-VERSION.yaml", "RUNTIME-VERSION.yaml"} {
		data, err := os.ReadFile(filepath.Join("..", "..", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo.Path(), name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	r, err := app.Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	w := NewWorkspace(r.Store(), r.ProjectID(), "runtime-regression")
	w.runtime = r
	w.workDir = r.ProjectRoot()
	l := &proposalMCPLease{id: "launch", owner: "owner", provider: "codex", active: true, dir: &briefingDir{}}
	w.proposalMCP.active = l
	w.tmuxActiveWins = map[string]*activeTmuxAgent{"marshal-chat": {provider: "codex", role: "marshal-chat", state: "working", briefingDir: l.dir}}
	t.Cleanup(func() { w.Close(); _ = r.Close() })
	if err := w.store.InitMCPProposals(t.Context()); err != nil {
		t.Fatal(err)
	}
	return w, l
}

func TestRuntimeRecoveryRefusalLoggedOnce(t *testing.T) {
	w, l := runtimeRecoveryFixture(t)
	c := projectcontext.Contract{FormatVersion: 1, Project: w.runtime.ProjectContractBinding(), ID: "architecture", Revision: 1, Purpose: "Cover the project", Components: []projectcontext.Component{{ID: "core", Responsibility: "Core", Paths: []string{"src"}}}, Invariants: []string{"Keep all code covered"}}
	c.ReviewedInventoryDigest = strings.Repeat("a", 64)
	p, _, err := w.store.AdmitMCPProjectContract(t.Context(), store.MCPProposal{ID: "mcp:regression", Launch: l.id, Owner: l.owner, Provider: l.provider, Submission: "draft"}, store.ProjectContractSubmission{Action: "project-contract", Contract: c})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.workDir, "uncovered.go"), []byte("package uncovered"), 0600); err != nil {
		t.Fatal(err)
	}
	rows, err := w.store.PendingMarshalProposals(t.Context(), w.projectID)
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err, p)
	}
	for range 10 {
		if !w.recoverBoundMCPMemory(rows[0]) {
			t.Fatal("not handled")
		}
	}
	if n := strings.Count(w.state.LastOutput, "Live MCP project contract recovery refused"); n != 1 {
		t.Fatalf("refusal logged %d times", n)
	}
	if !w.permissions.queue.Empty() {
		t.Fatal("invalid draft was queued")
	}
	if err := os.Remove(filepath.Join(w.workDir, "uncovered.go")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.resumeProjectContractMCP(t.Context(), l, p); err != nil {
		t.Fatal(err)
	}
	if w.permissions.queue.Empty() || l.recoveryRefusals[p.ID] {
		t.Fatal("corrected explicit retry did not recover")
	}

}

func TestRuntimeDesignReviewBrokerPollsOutsideRequest(t *testing.T) {
	w, l := runtimeRecoveryFixture(t)
	bin := t.TempDir()
	calls := filepath.Join(t.TempDir(), "calls")
	t.Setenv("MARSHAL_TEST_CALLS", calls)
	script := `#!/bin/sh
printf 'launch\n' >> "$MARSHAL_TEST_CALLS"
sleep 0.1
printf '%s\n' '{"type":"result","session_id":"fresh-review","structured_output":{"findings":"Review completed."}}'
`
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	raw := json.RawMessage(`{"provider":"claude","draft":"Review this design","reading_list":["README.md"]}`)
	requestCtx, cancel := context.WithCancel(t.Context())
	out, err := w.handleProposalMCP(requestCtx, l, "design_review", raw)
	cancel()
	if err != nil || !strings.Contains(string(out), `"state":"pending"`) {
		t.Fatal(string(out), err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		out, err = w.handleProposalMCP(t.Context(), l, "design_review", raw)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(out), `"state":"completed"`) {
			break
		}
		if strings.Contains(string(out), `"state":"failed"`) || time.Now().After(deadline) {
			t.Fatal(string(out))
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(string(out), "fresh-review") {
		t.Fatal(string(out))
	}
	data, err := os.ReadFile(calls)
	if err != nil || string(data) != "launch\n" {
		t.Fatalf("poll relaunched provider: %q %v", data, err)
	}
}

func TestRuntimeDesignReviewLeaseRevocationStopsReviewer(t *testing.T) {
	w, l := runtimeRecoveryFixture(t)
	l.cleanup = func() {}
	bin := t.TempDir()
	started := filepath.Join(t.TempDir(), "started")
	t.Setenv("MARSHAL_TEST_REVIEW_STARTED", started)
	script := "#!/bin/sh\ntouch \"$MARSHAL_TEST_REVIEW_STARTED\"\nsleep 30\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	if _, err := w.handleProposalMCP(t.Context(), l, "design_review", json.RawMessage(`{"provider":"claude","draft":"Review design"}`)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("reviewer did not launch")
		}
		time.Sleep(10 * time.Millisecond)
	}
	w.revokeProposalLease(l)
	for {
		var state string
		for _, job := range l.designReviews {
			job.mu.Lock()
			state = job.state
			job.mu.Unlock()
		}
		if state == "failed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("revocation did not stop reviewer")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
