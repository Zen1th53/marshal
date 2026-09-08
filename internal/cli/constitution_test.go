package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/constitution"
)

func runConstitutionCLI(t *testing.T, root string, args ...string) (string, int) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Execute(context.Background(), root, args, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		return stderr.String(), code
	}
	return stdout.String(), code
}

func constitutionCLIRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if _, err := app.Bootstrap(context.Background(), root); err != nil {
		t.Skipf("bootstrap unavailable in this environment: %v", err)
	}
	return root
}

func TestConstitutionVersionCommandReportsRulesInForce(t *testing.T) {
	root := constitutionCLIRoot(t)
	out, code := runConstitutionCLI(t, root, "--json", "constitution", "version")
	if code != 0 {
		t.Fatalf("constitution version exited %d: %s", code, out)
	}
	var payload struct {
		Version        string `json:"constitution_version"`
		Digest         string `json:"invariant_digest"`
		InvariantCount int    `json:"invariant_count"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("decode output: %v (%s)", err, out)
	}
	if payload.Version != constitution.Current.String() {
		t.Fatalf("reported constitution %s, want %s", payload.Version, constitution.Current)
	}
	// The digest is what distinguishes two builds that both claim 1.0.0 but
	// enforce different invariant sets.
	if payload.Digest == "" {
		t.Fatal("no invariant digest reported, so a reader cannot tell which rules were in force")
	}
	if payload.InvariantCount == 0 {
		t.Fatal("no invariants reported")
	}
}

func TestConstitutionInvariantsCommandListsUserSafeExplanations(t *testing.T) {
	root := constitutionCLIRoot(t)
	out, code := runConstitutionCLI(t, root, "--json", "constitution", "invariants")
	if code != 0 {
		t.Fatalf("constitution invariants exited %d: %s", code, out)
	}
	var invariants []struct {
		ID          string `json:"id"`
		Article     string `json:"article"`
		Severity    string `json:"severity"`
		Explanation string `json:"explanation"`
	}
	if err := json.Unmarshal([]byte(out), &invariants); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if len(invariants) != constitution.Default().Len() {
		t.Fatalf("listed %d invariants, want %d", len(invariants), constitution.Default().Len())
	}
	for _, invariant := range invariants {
		if invariant.ID == "" || invariant.Article == "" || invariant.Severity == "" {
			t.Fatalf("an invariant is missing its identity: %+v", invariant)
		}
		if invariant.Explanation == "" {
			t.Fatalf("invariant %s has no user-facing explanation", invariant.ID)
		}
	}
}

// An unviolated session must be reported as not suspended, distinctly from an
// empty list, so a reader is not left inferring which claim is being made.
func TestConstitutionViolationsReportsSuspensionExplicitly(t *testing.T) {
	root := constitutionCLIRoot(t)
	out, code := runConstitutionCLI(t, root, "--json", "constitution", "violations", "SESSION-none")
	if code != 0 {
		t.Fatalf("constitution violations exited %d: %s", code, out)
	}
	var payload struct {
		SessionID  string           `json:"session_id"`
		Suspended  bool             `json:"suspended"`
		Violations []map[string]any `json:"violations"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("decode output: %v (%s)", err, out)
	}
	if payload.SessionID != "SESSION-none" {
		t.Fatalf("reported session %q", payload.SessionID)
	}
	if payload.Suspended {
		t.Fatal("a session with no violations was reported as suspended")
	}
	if len(payload.Violations) != 0 {
		t.Fatalf("reported %d violations for a clean session", len(payload.Violations))
	}
}

// A real violation surfaces through the CLI with its mandated response, so the
// command reports backend truth rather than a separate view of it.
func TestConstitutionViolationsSurfacesRecordedBreach(t *testing.T) {
	root := constitutionCLIRoot(t)
	ctx := context.Background()
	runtime, err := app.Open(ctx, root)
	if err != nil {
		t.Fatalf("open runtime: %v", err)
	}
	service := runtime.Constitution()

	envelope := constitution.Envelope{
		DecisionID: "cli-violation", ConstitutionVersion: constitution.Current, Process: 5,
		ProjectID: runtime.ProjectID(), SessionID: "SESSION-cli", Actor: "actor-1",
		Surface: constitution.SurfaceCLI, Mode: constitution.ModeStandard,
		Domain: constitution.DomainFileMutation, Action: "write", BlastRadius: 1,
		Reversibility: constitution.ReversibleInternal, StateDigest: "sha256:s",
		RequestedAt: time.Now().UTC(),
	}
	state := constitution.RuntimeState{
		SandboxAvailable: true, NetworkEnforced: true, AuthorizedActor: true,
		EvidencePresent: true, EvidenceFresh: true,
		HarnessGovernance:  constitution.GovernanceVerified,
		ForeignProjectRefs: []string{"PROJECT-other"},
	}
	if _, err := service.Decide(ctx, app.DecideRequest{Envelope: envelope, State: state}); err != nil {
		t.Fatalf("decide: %v", err)
	}
	runtime.Close()

	out, code := runConstitutionCLI(t, root, "--json", "constitution", "violations", "SESSION-cli")
	if code != 0 {
		t.Fatalf("constitution violations exited %d: %s", code, out)
	}
	if !strings.Contains(out, "CROSS_PROJECT_LEAK") {
		t.Fatalf("the recorded breach did not surface through the CLI: %s", out)
	}
	if !strings.Contains(out, `"suspended": true`) {
		t.Fatalf("the CLI did not report the session as suspended: %s", out)
	}

	// The decisions view reports the same outcome the backend recorded.
	decisions, code := runConstitutionCLI(t, root, "--json", "constitution", "decisions", "SESSION-cli")
	if code != 0 {
		t.Fatalf("constitution decisions exited %d: %s", code, decisions)
	}
	if !strings.Contains(decisions, "BLOCK") {
		t.Fatalf("the CLI did not report the blocked outcome: %s", decisions)
	}
}

// The command surface is read-only: there is no subcommand that grants,
// waives or resolves anything, because one would be a way around the control
// the violation represents.
func TestConstitutionCommandOffersNoMutation(t *testing.T) {
	root := constitutionCLIRoot(t)
	for _, attempt := range []string{"resolve", "waive", "approve", "clear", "grant", "override", "suspend"} {
		out, code := runConstitutionCLI(t, root, "constitution", attempt, "SESSION-cli")
		if code == 0 {
			t.Fatalf("constitution %s was accepted; the command surface must be read-only (%s)", attempt, out)
		}
	}
	if out, code := runConstitutionCLI(t, root, "constitution"); code == 0 {
		t.Fatalf("constitution with no subcommand succeeded: %s", out)
	}
	// The session-scoped views require a session rather than defaulting to one.
	if out, code := runConstitutionCLI(t, root, "constitution", "violations"); code == 0 {
		t.Fatalf("constitution violations succeeded with no session: %s", out)
	}
}

func TestVersionCommandReportsConstitutionVersion(t *testing.T) {
	root := constitutionCLIRoot(t)
	out, code := runConstitutionCLI(t, root, "--json", "version")
	if code != 0 {
		t.Fatalf("version exited %d: %s", code, out)
	}
	if !strings.Contains(out, constitution.Current.String()) {
		t.Fatalf("version output omits the constitution version: %s", out)
	}
}
