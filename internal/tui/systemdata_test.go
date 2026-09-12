package tui

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/resources"
)

type fakeSystemReader struct {
	status RuntimeStatus
	err    error
	counts map[string]int
	tokens []auth.TokenRecord
}

func (f fakeSystemReader) RuntimeStatus(context.Context) (RuntimeStatus, error) {
	return f.status, f.err
}
func (f fakeSystemReader) RuntimeInstanceID() string                       { return "instance-1" }
func (f fakeSystemReader) StoreSchemaVersion(context.Context) (int, error) { return 12, f.err }
func (f fakeSystemReader) StoreIntegrity(context.Context) error            { return f.err }
func (f fakeSystemReader) ObjectCount(_ context.Context, table string) (int, error) {
	if f.err != nil {
		return 0, f.err
	}
	return f.counts[table], nil
}
func (f fakeSystemReader) CollectResources(context.Context) (resources.Snapshot, error) {
	if f.err != nil {
		return resources.Snapshot{}, f.err
	}
	return resources.Snapshot{CPU: resources.CPU{Logical: 8, Architecture: "amd64"}, Memory: resources.Memory{TotalBytes: 16}, Storage: resources.Storage{TotalBytes: 32}, Ollama: resources.Ollama{Status: "NOT_AVAILABLE"}, Health: resources.Health{Overall: resources.StatusOK}}, nil
}
func (f fakeSystemReader) TokenMetadata(context.Context) ([]auth.TokenRecord, error) {
	return f.tokens, f.err
}

func TestSystemWithoutReaderNeverReportsMeasuredState(t *testing.T) {
	snap := (&SystemFeed{Now: fixedClock()}).ReadSystem(context.Background())
	for name, value := range map[string]Value{"runtime": snap.Runtime, "schema": snap.SchemaVersion, "tokens": snap.TokensStatus, "cpu": snap.CPU, "release": snap.Release} {
		if value.Status.IsSuccess() {
			t.Fatalf("%s is known without a reader: %q", name, value.Display())
		}
		if value.Reason == "" {
			t.Fatalf("%s has no unavailable reason", name)
		}
	}
}

func TestSystemReaderConvertsMeasuredZeroToKnown(t *testing.T) {
	feed := &SystemFeed{Reader: fakeSystemReader{status: RuntimeStatus{ProjectID: "project-1"}, counts: map[string]int{}}, Now: fixedClock()}
	snap := feed.ReadSystem(context.Background())
	if snap.Agents.Status != TruthKnown || snap.Agents.Text != "0" {
		t.Fatalf("agents = %#v, want measured zero", snap.Agents)
	}
	if snap.Findings.Status != TruthKnown || snap.Findings.Text != "0" {
		t.Fatalf("findings = %#v, want measured zero", snap.Findings)
	}
	if snap.Storage.Status != TruthKnown {
		t.Fatalf("storage was not read: %q", snap.Storage.Display())
	}
}

func TestSystemUnreadableTokensAreNotEmpty(t *testing.T) {
	feed := &SystemFeed{Reader: fakeSystemReader{err: errors.New("auth file unavailable")}, Now: fixedClock()}
	snap := feed.ReadSystem(context.Background())
	if snap.TokensStatus.Status == TruthEmpty || snap.TokensStatus.Status.IsSuccess() {
		t.Fatalf("unreadable tokens rendered as %q", snap.TokensStatus.Display())
	}
}

func TestSystemNeverRendersTokenDigestOrPlaintext(t *testing.T) {
	feed := &SystemFeed{Reader: fakeSystemReader{status: RuntimeStatus{ProjectID: "project-1"}, counts: map[string]int{}, tokens: []auth.TokenRecord{{ID: "TOKEN-1", Name: "agent", Digest: "secret-digest", Kind: auth.KindA2AAgent, CreatedAt: time.Date(2026, 9, 10, 1, 0, 0, 0, time.UTC)}}}, Now: fixedClock()}
	snap := feed.ReadSystem(context.Background())
	if len(snap.Tokens) != 1 {
		t.Fatalf("got %d token rows", len(snap.Tokens))
	}
	if !snap.Tokens[0].Credential.Redacted {
		t.Fatal("credential indicator is not redacted")
	}
	if got := tokensScreen(snap).Fields; len(got) == 0 {
		t.Fatal("token screen has no fields")
	}
	for _, field := range tokensScreen(snap).Fields {
		if field.Value.Text == "secret-digest" {
			t.Fatal("token digest reached screen content")
		}
	}
}

func TestSystemRendererRegistryContainsOnlyStandaloneSystemScreens(t *testing.T) {
	renderers := systemScreens()
	if len(renderers) != 84 {
		t.Fatalf("System renderer count = %d, want 84 explicit standalone screens", len(renderers))
	}
	for id, render := range renderers {
		if render == nil {
			t.Fatalf("%s has a nil renderer", id)
		}
	}
	for _, action := range []string{"CTUI-0705", "CTUI-0741", "CTUI-0750", "CTUI-0761", "CTUI-0762", "CTUI-0763", "CTUI-0767", "CTUI-0769", "CTUI-0771", "CTUI-0774", "CTUI-0781", "CTUI-0782", "CTUI-0783", "CTUI-0785", "CTUI-0787", "CTUI-0803"} {
		if _, ok := renderers[action]; ok {
			t.Fatalf("action %s was incorrectly registered as a standalone screen", action)
		}
	}
}

// --- bindings Codex reported as unavailable that do exist ---

// A state backup proves itself by the database digest, not by a path. The path
// is deliberately never shown: it would disclose the runtime directory, and it
// proves only that a name was chosen.
func TestStateBackupProvesItselfByDigestNotPath(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()

	binding := source.Bindings()["CTUI-0769"]
	if !binding.Bound() {
		t.Fatal("creating a state backup is not bound, but Runtime.BackupState exists")
	}
	if binding.Safety != SafetyDestructive {
		t.Fatalf("a backup is classed %s, want destructive", binding.Safety)
	}

	c := &Confirmation{}
	if err := c.Begin(ctx, binding, ActionRequest{Action: "CTUI-0769"}); err != nil {
		t.Fatalf("begin: %v", err)
	}
	c.MoveSelection(1)
	c.Acknowledge()
	outcome, err := c.Submit(ctx)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if outcome.Verdict != VerdictPass {
		t.Fatalf("a backup produced %s: %s", outcome.Verdict, outcome.Detail)
	}
	if n := atomic.LoadInt32(&auth.backups); n != 1 {
		t.Fatalf("the runtime was called %d times", n)
	}
	// The digest is the proof; no path appears anywhere.
	if !strings.Contains(outcome.Proof.Display(), "sha256") {
		t.Fatalf("the proof is not the digest: %q", outcome.Proof.Display())
	}
	for _, field := range []string{outcome.Detail, outcome.Evidence, outcome.Proof.Display()} {
		if strings.Contains(field, "/home/") || strings.Contains(field, ".marshal/") {
			t.Fatalf("a runtime path was disclosed: %q", field)
		}
	}
}

// A backup that reported no digest has not demonstrated anything.
func TestBackupWithoutADigestIsNotSuccess(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()

	auth.mu.Lock()
	auth.backupProof = BackupProof{}
	auth.mu.Unlock()

	outcome, _ := source.executeBackupState(ctx,
		ActionRequest{Action: "CTUI-0769", Target: Target{Kind: "session", ID: "sess-1"}})
	if outcome.Verdict == VerdictPass {
		t.Fatal("a backup with no digest reported success")
	}
	if outcome.Proof.Status.IsSuccess() {
		t.Fatal("a digestless backup carries a successful proof")
	}
}

// Collecting artifacts refuses when nothing is eligible and proves itself when
// something is — the same shape as the worktree collection.
func TestArtifactCollectionProvesItselfAndRefusesWhenEmpty(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()

	binding := source.Bindings()["CTUI-0774"]
	if !binding.Bound() {
		t.Fatal("collecting artifacts is not bound, but Runtime.GCArtifacts exists")
	}
	if binding.Safety != SafetyDestructive {
		t.Fatalf("artifact collection is classed %s, want destructive", binding.Safety)
	}

	empty, _ := source.executeGCArtifacts(ctx,
		ActionRequest{Action: "CTUI-0774", Target: Target{Kind: "session", ID: "sess-1"}})
	if empty.Verdict != VerdictBlocked {
		t.Fatalf("collecting nothing produced %s", empty.Verdict)
	}
	if n := atomic.LoadInt32(&auth.artifactGCs); n != 0 {
		t.Fatalf("an empty collection still ran %d times", n)
	}

	auth.mu.Lock()
	auth.eligibleArtifacts = 5
	auth.mu.Unlock()

	outcome, err := source.executeGCArtifacts(ctx,
		ActionRequest{Action: "CTUI-0774", Target: Target{Kind: "session", ID: "sess-1"}})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if outcome.Verdict != VerdictPass {
		t.Fatalf("a real collection produced %s: %s", outcome.Verdict, outcome.Detail)
	}
	if n := atomic.LoadInt32(&auth.artifactGCs); n != 1 {
		t.Fatalf("collection ran %d times", n)
	}
}
