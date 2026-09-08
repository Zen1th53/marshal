package app

import (
	"context"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/constitution"
)

func constitutionRuntime(t *testing.T) *Runtime {
	t.Helper()
	repo := runtimeRepo(t)
	ctx := context.Background()
	if _, err := Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	runtime, err := Open(ctx, repo.Path())
	if err != nil {
		t.Fatalf("open runtime: %v", err)
	}
	t.Cleanup(func() { runtime.Close() })
	return runtime
}

func runtimeEnvelope(runtime *Runtime, id string) constitution.Envelope {
	return constitution.Envelope{
		DecisionID:          id,
		ConstitutionVersion: constitution.Current,
		Process:             5,
		ProjectID:           runtime.ProjectID(),
		SessionID:           "SESSION-const-1",
		Actor:               "developer-1",
		ActorRole:           "developer",
		Surface:             constitution.SurfaceCore,
		Mode:                constitution.ModeStandard,
		Domain:              constitution.DomainFileMutation,
		Action:              "write internal/foo.go",
		Scope:               []string{"internal/foo.go"},
		BlastRadius:         1,
		Reversibility:       constitution.ReversibleInternal,
		StateDigest:         "sha256:state",
		RequestedAt:         time.Now().UTC(),
	}
}

func permissiveState() constitution.RuntimeState {
	return constitution.RuntimeState{
		SandboxAvailable:  true,
		NetworkEnforced:   true,
		AuthorizedActor:   true,
		EvidencePresent:   true,
		EvidenceFresh:     true,
		HarnessGovernance: constitution.GovernanceVerified,
	}
}

func TestRuntimeBindsSessionConstitution(t *testing.T) {
	runtime := constitutionRuntime(t)
	ctx := context.Background()
	service := runtime.Constitution()

	version, err := service.BindSession(ctx, "SESSION-1", runtime.ProjectID(), constitution.ModeStandard)
	if err != nil {
		t.Fatalf("bind session: %v", err)
	}
	if version.Compare(constitution.Current) != 0 {
		t.Fatalf("session bound to %s, want %s", version, constitution.Current)
	}

	// Re-binding the same session to the same version is idempotent, so a
	// restart does not fail.
	if _, err := service.BindSession(ctx, "SESSION-1", runtime.ProjectID(), constitution.ModeStandard); err != nil {
		t.Fatalf("re-binding the same session failed: %v", err)
	}

	stored, found, err := service.SessionVersion(ctx, "SESSION-1")
	if err != nil || !found {
		t.Fatalf("session binding was not persisted: found=%v err=%v", found, err)
	}
	if stored.Compare(constitution.Current) != 0 {
		t.Fatalf("persisted version %s, want %s", stored, constitution.Current)
	}

	if _, found, err := service.SessionVersion(ctx, "SESSION-unbound"); err != nil || found {
		t.Fatal("an unbound session reported a constitution binding")
	}
	if service.InvariantDigest() == "" {
		t.Fatal("the invariant digest is empty, so a report cannot prove which rules were in force")
	}
}

func TestRuntimeRejectsUnknownSessionMode(t *testing.T) {
	runtime := constitutionRuntime(t)
	service := runtime.Constitution()
	if _, err := service.BindSession(context.Background(), "S", runtime.ProjectID(), "godmode"); err == nil {
		t.Fatal("an undefined session mode was accepted")
	}
	if _, err := service.BindSession(context.Background(), "", runtime.ProjectID(), constitution.ModeStandard); err == nil {
		t.Fatal("a session with no ID was bound")
	}
}

// A decision is not complete until it is recorded: a verdict nobody can audit
// is not authority MARSHAL can account for.
func TestRuntimeDecisionIsPersisted(t *testing.T) {
	runtime := constitutionRuntime(t)
	ctx := context.Background()
	service := runtime.Constitution()

	envelope := runtimeEnvelope(runtime, "dec-persist")
	result, err := service.Decide(ctx, DecideRequest{Envelope: envelope, State: permissiveState()})
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if !result.Permitted() {
		t.Fatalf("a clean decision was refused: %s %+v", result.Verdict.Outcome, result.Verdict.Findings)
	}

	decisions, err := service.SessionDecisions(ctx, envelope.SessionID, 10)
	if err != nil {
		t.Fatalf("read decisions: %v", err)
	}
	if len(decisions) != 1 {
		t.Fatalf("recorded %d decisions, want 1", len(decisions))
	}
	recorded := decisions[0]
	if recorded.Outcome != string(constitution.OutcomeAllow) {
		t.Fatalf("recorded outcome %s, want ALLOW", recorded.Outcome)
	}
	if recorded.BindingDigest != envelope.BindingDigest() {
		t.Fatal("the recorded approval binding does not match the decision it was reached for")
	}
	if recorded.Actor != envelope.Actor || recorded.Domain != string(envelope.Domain) {
		t.Fatalf("the recorded decision does not describe the action taken: %+v", recorded)
	}
}

// Re-deciding the same decision updates its record rather than forging a
// second history, so a retry after a crash does not duplicate the audit trail.
func TestRuntimeDecisionRecordIsIdempotent(t *testing.T) {
	runtime := constitutionRuntime(t)
	ctx := context.Background()
	service := runtime.Constitution()
	envelope := runtimeEnvelope(runtime, "dec-retry")

	for i := 0; i < 3; i++ {
		if _, err := service.Decide(ctx, DecideRequest{Envelope: envelope, State: permissiveState()}); err != nil {
			t.Fatalf("decide pass %d: %v", i, err)
		}
	}
	decisions, err := service.SessionDecisions(ctx, envelope.SessionID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(decisions) != 1 {
		t.Fatalf("a retried decision produced %d audit rows, want 1", len(decisions))
	}
}

// A blocked decision records both the verdict and the violation it evidenced,
// and the session is suspended when the breach warrants it.
func TestRuntimeRecordsViolationsAndSuspendsSession(t *testing.T) {
	runtime := constitutionRuntime(t)
	ctx := context.Background()
	service := runtime.Constitution()

	envelope := runtimeEnvelope(runtime, "dec-violation")
	state := permissiveState()
	state.ForeignProjectRefs = []string{"PROJECT-other/file.md"}

	result, err := service.Decide(ctx, DecideRequest{Envelope: envelope, State: state})
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if result.Permitted() {
		t.Fatal("a cross-project reference was permitted")
	}
	if len(result.Violations) == 0 {
		t.Fatal("the breach was not recorded as a violation")
	}

	open, err := service.OpenViolations(ctx, envelope.SessionID)
	if err != nil {
		t.Fatalf("read violations: %v", err)
	}
	if len(open) == 0 {
		t.Fatal("the violation was not persisted")
	}
	if open[0].ProjectID != runtime.ProjectID() {
		t.Fatal("the persisted violation is not bound to its project")
	}

	suspended, err := service.SessionSuspended(ctx, envelope.SessionID)
	if err != nil {
		t.Fatalf("check suspension: %v", err)
	}
	if !suspended {
		t.Fatal("a cross-project leak did not suspend the session")
	}

	// A session with no violations is not suspended.
	clean, err := service.SessionSuspended(ctx, "SESSION-clean")
	if err != nil {
		t.Fatal(err)
	}
	if clean {
		t.Fatal("a session with no violations was reported as suspended")
	}
}

// A session's recorded binding outranks whatever version a caller asserts, so
// a surface cannot shop for laxer semantics.
func TestRuntimeSessionBindingOutranksAssertedVersion(t *testing.T) {
	runtime := constitutionRuntime(t)
	ctx := context.Background()
	service := runtime.Constitution()

	envelope := runtimeEnvelope(runtime, "dec-assert")
	if _, err := service.BindSession(ctx, envelope.SessionID, runtime.ProjectID(), constitution.ModeStandard); err != nil {
		t.Fatalf("bind session: %v", err)
	}

	// The caller asserts a different version than the session is bound to.
	envelope.ConstitutionVersion = constitution.Version{Major: constitution.Current.Major, Minor: 99}
	result, err := service.Decide(ctx, DecideRequest{Envelope: envelope, State: permissiveState()})
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if result.Verdict.ConstitutionVersion.Compare(constitution.Current) != 0 {
		t.Fatalf("the asserted version %s was honoured over the session binding",
			result.Verdict.ConstitutionVersion)
	}
}

// The same material action reaches the same verdict on every surface, through
// the real runtime rather than only in the gate library.
func TestRuntimeCrossSurfaceParity(t *testing.T) {
	runtime := constitutionRuntime(t)
	ctx := context.Background()
	service := runtime.Constitution()

	surfaces := []constitution.Surface{
		constitution.SurfaceTUI, constitution.SurfaceCLI, constitution.SurfaceWeb,
		constitution.SurfaceMCP, constitution.SurfaceA2A, constitution.SurfaceCore,
	}
	state := permissiveState()
	state.SandboxAvailable = false // a condition every surface must refuse

	var reference DecideResult
	for i, surface := range surfaces {
		envelope := runtimeEnvelope(runtime, "dec-parity-"+string(surface))
		envelope.Surface = surface
		envelope.Domain = constitution.DomainShell
		result, err := service.Decide(ctx, DecideRequest{Envelope: envelope, State: state})
		if err != nil {
			t.Fatalf("decide on %s: %v", surface, err)
		}
		if result.Permitted() {
			t.Fatalf("surface %s permitted execution with no sandbox", surface)
		}
		if i == 0 {
			reference = result
			continue
		}
		if result.Verdict.Outcome != reference.Verdict.Outcome ||
			result.Verdict.Reason != reference.Verdict.Reason {
			t.Fatalf("surface %s reached %s/%s but %s reached %s/%s",
				surface, result.Verdict.Outcome, result.Verdict.Reason,
				surfaces[0], reference.Verdict.Outcome, reference.Verdict.Reason)
		}
	}
}

// The runtime supplies its own constitution version, so a caller cannot claim
// to run under rules this build does not implement.
func TestRuntimeSuppliesItsOwnConstitutionVersion(t *testing.T) {
	runtime := constitutionRuntime(t)
	ctx := context.Background()
	service := runtime.Constitution()

	envelope := runtimeEnvelope(runtime, "dec-forged")
	state := permissiveState()
	// A caller attempts to declare that the runtime implements a version that
	// would make its own future-dated envelope acceptable.
	state.RuntimeConstitution = constitution.Version{Major: 99}
	envelope.ConstitutionVersion = constitution.Version{Major: 99}

	result, err := service.Decide(ctx, DecideRequest{Envelope: envelope, State: state})
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if result.Permitted() {
		t.Fatal("a caller-declared runtime constitution version was honoured")
	}
}

func TestRuntimeVersionAndRegistryAreExposed(t *testing.T) {
	runtime := constitutionRuntime(t)
	service := runtime.Constitution()
	if service.Version().Compare(constitution.Current) != 0 {
		t.Fatalf("runtime reports constitution %s, want %s", service.Version(), constitution.Current)
	}
	if service.Registry() == nil || service.Registry().Len() == 0 {
		t.Fatal("the runtime exposes no invariant registry")
	}
}
