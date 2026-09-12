package tui

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/authz"
)

// Security is where an optimistic default is most dangerous. These tests assert
// that nothing in the section turns a missing answer into a reassuring one.

type fakeSecurityReader struct {
	version    string
	versionErr error
	bindings   []authz.RoleBinding
	bindErr    error
	sandbox    SandboxState
	sandboxErr error
	leases     int
	leaseErr   error
}

func (f fakeSecurityReader) ConstitutionVersion(context.Context) (string, error) {
	return f.version, f.versionErr
}
func (f fakeSecurityReader) RoleBindings(context.Context) ([]authz.RoleBinding, error) {
	return f.bindings, f.bindErr
}
func (f fakeSecurityReader) SandboxState(context.Context) (SandboxState, error) {
	return f.sandbox, f.sandboxErr
}
func (f fakeSecurityReader) SecretLeases(context.Context) (int, error) {
	return f.leases, f.leaseErr
}

func testSecurity(t *testing.T, reader SecurityReader) *SecurityFeed {
	t.Helper()
	return &SecurityFeed{Reader: reader, ProjectID: "proj-1", Now: fixedClock()}
}

// With nothing attached, every field says it was not checked. None says "fine".
func TestSecurityWithoutReadersNeverReassures(t *testing.T) {
	snap := (&SecurityFeed{Now: fixedClock()}).ReadSecurity(context.Background())

	for name, v := range map[string]Value{
		"constitution": snap.Constitution, "grants": snap.GrantsStatus,
		"sandbox": snap.Sandbox, "network": snap.Network, "secrets": snap.SecretLeases,
	} {
		if v.Status.IsSuccess() {
			t.Fatalf("%s reports a known value with nothing attached: %q",
				name, v.Display())
		}
		if v.Reason == "" {
			t.Fatalf("%s gives no reason", name)
		}
		// Nothing may render as an absence of problems.
		lower := strings.ToLower(v.Display())
		for _, reassuring := range []string{"none", "no violations", "secure", "ok"} {
			if lower == reassuring {
				t.Fatalf("%s rendered reassuringly as %q", name, v.Display())
			}
		}
	}
}

// An unreadable grant list is not an empty one: rendering it as empty would say
// nobody has access when the truth is that nobody looked.
func TestUnreadableGrantsAreNotReportedAsNoAccess(t *testing.T) {
	snap := testSecurity(t, fakeSecurityReader{
		bindErr: errors.New("the authorization store is unreachable"),
	}).ReadSecurity(context.Background())

	if snap.GrantsStatus.Status == TruthEmpty {
		t.Fatal("an unreadable grant list rendered as EMPTY, which reads as no access")
	}
	if snap.GrantsStatus.Status.IsSuccess() {
		t.Fatal("an unreadable grant list reported success")
	}
	if !strings.Contains(snap.GrantsStatus.Reason, "unknown, not none") {
		t.Fatalf("the distinction is not stated: %q", snap.GrantsStatus.Reason)
	}
}

// An unprobeable sandbox must not read as a working one. Unavailable
// enforcement fails closed.
func TestUnprobeableSandboxIsUnverifiedNotSafe(t *testing.T) {
	snap := testSecurity(t, fakeSecurityReader{
		sandboxErr: errors.New("the runtime does not expose it"),
	}).ReadSecurity(context.Background())

	if snap.Sandbox.Status.IsSuccess() {
		t.Fatalf("an unprobeable sandbox reported %q", snap.Sandbox.Display())
	}
	if !strings.Contains(snap.Sandbox.Reason, "unprotected") {
		t.Fatalf("the sandbox reason does not fail closed: %q", snap.Sandbox.Reason)
	}
	if snap.Network.Status.IsSuccess() {
		t.Fatal("an unprobeable sandbox reported working egress enforcement")
	}
}

// An absent sandbox is a real, serious answer rather than a read failure.
func TestAbsentSandboxIsBlockedWithAReason(t *testing.T) {
	snap := testSecurity(t, fakeSecurityReader{sandbox: SandboxState{
		Available: false, Reason: "bubblewrap is not installed",
	}}).ReadSecurity(context.Background())

	if snap.Sandbox.Status != TruthBlocked {
		t.Fatalf("an absent sandbox reported %s", snap.Sandbox.Status.Label())
	}
	if !strings.Contains(snap.Sandbox.Display(), "bubblewrap") {
		t.Fatalf("the reason was lost: %q", snap.Sandbox.Display())
	}
}

// Unenforceable egress fails closed rather than reading as permitted.
func TestUnenforceableEgressFailsClosed(t *testing.T) {
	snap := testSecurity(t, fakeSecurityReader{sandbox: SandboxState{
		Available: true, Backend: "bwrap", NetworkEnforced: false,
	}}).ReadSecurity(context.Background())

	if snap.Network.Status.IsSuccess() {
		t.Fatalf("unenforceable egress reported %q", snap.Network.Display())
	}
	if !strings.Contains(snap.Network.Display(), "fails closed") {
		t.Fatalf("the egress state does not say it fails closed: %q",
			snap.Network.Display())
	}
}

// A revoked grant is shown, not hidden: the audit trail is the point.
func TestRevokedGrantsRemainVisible(t *testing.T) {
	revoked := time.Date(2026, 9, 10, 11, 0, 0, 0, time.UTC)
	snap := testSecurity(t, fakeSecurityReader{bindings: []authz.RoleBinding{
		{ID: "grant-1", PrincipalID: "agent-1", Role: "developer", ScopeID: "task-1"},
		{ID: "grant-2", PrincipalID: "agent-2", Role: "reviewer", ScopeID: "task-1",
			RevokedAt: &revoked},
	}}).ReadSecurity(context.Background())

	if len(snap.Grants) != 2 {
		t.Fatalf("got %d grants, want both the active and the revoked one", len(snap.Grants))
	}
	if !strings.Contains(snap.GrantsStatus.Display(), "1 active") {
		t.Fatalf("the active count is wrong: %q", snap.GrantsStatus.Display())
	}
	// The revoked one is marked rather than silently dropped.
	var revokedRow GrantRow
	for _, grant := range snap.Grants {
		if grant.ID.Display() == "grant-2" {
			revokedRow = grant
		}
	}
	if revokedRow.Standing.Status.IsSuccess() {
		t.Fatalf("a revoked grant reads as in force: %q", revokedRow.Standing.Display())
	}
}

// An unrecognised binding state must not read as active.
func TestUnrecognisedGrantStateIsUnknownNotActive(t *testing.T) {
	snap := testSecurity(t, fakeSecurityReader{bindings: []authz.RoleBinding{
		{ID: "grant-1", PrincipalID: "a", Role: "r", ScopeID: "s",
			State: authz.BindingState("SOMETHING_NEW")},
	}}).ReadSecurity(context.Background())

	standing := snap.Grants[0].Standing
	if standing.Status.IsSuccess() {
		t.Fatalf("an unrecognised grant state reads as active: %q", standing.Display())
	}
	if standing.Status != TruthUnknown {
		t.Fatalf("an unrecognised grant state reports %s", standing.Status.Label())
	}
}

// Secret leases are counted and never listed; an unknown count is not zero.
func TestSecretLeasesAreCountedNotListed(t *testing.T) {
	counted := testSecurity(t, fakeSecurityReader{leases: 3}).
		ReadSecurity(context.Background())
	if counted.SecretLeases.Display() != "3" {
		t.Fatalf("the lease count rendered as %q", counted.SecretLeases.Display())
	}
	if !strings.Contains(counted.SecretLeases.Reason, "never listed") {
		t.Fatalf("the lease field does not say content stays away: %q",
			counted.SecretLeases.Reason)
	}

	unknown := testSecurity(t, fakeSecurityReader{
		leaseErr: errors.New("not enumerable"),
	}).ReadSecurity(context.Background())
	if unknown.SecretLeases.Display() == "0" {
		t.Fatal("an uncountable lease set rendered as zero, which reads as no secrets")
	}
	if unknown.SecretLeases.Status.IsSuccess() {
		t.Fatal("an uncountable lease set reported success")
	}
}

// Granting access needs a principal, role and scope this screen does not
// collect: granting from invented arguments would hand somebody authority.
func TestGrantingAccessIsUnavailable(t *testing.T) {
	source, _ := testControl(t)
	binding := source.Bindings()["CTUI-0664"]

	if binding.Bound() {
		t.Fatal("granting a capability is bound to a screen that collects no arguments")
	}
	if !strings.Contains(binding.Requires, "principal") {
		t.Fatalf("the refusal does not say what is missing: %q", binding.Requires)
	}
}

// Revoking is destructive, bound, and proves itself.
func TestRevokingAGrantProvesItself(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()

	binding := source.Bindings()["CTUI-0665"]
	if !binding.Bound() {
		t.Fatal("revoking a grant is not bound")
	}
	if binding.Safety != SafetyDestructive {
		t.Fatalf("revoking a grant is classed %s, want destructive", binding.Safety)
	}

	source.SelectGrant("grant-1")
	c := &Confirmation{}
	if err := c.Begin(ctx, binding, ActionRequest{
		Action: "CTUI-0665", ApprovalID: "app-1"}); err != nil {
		t.Fatalf("begin: %v", err)
	}
	c.MoveSelection(1)
	c.Acknowledge()
	outcome, err := c.Submit(ctx)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if outcome.Verdict != VerdictPass {
		t.Fatalf("a revocation produced %s: %s", outcome.Verdict, outcome.Detail)
	}
	if n := atomic.LoadInt32(&auth.revocations); n != 1 {
		t.Fatalf("the store was called %d times", n)
	}
}

// A revocation the store did not record must not report success: the principal
// may still hold access.
func TestUnrecordedRevocationIsNotSuccess(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()

	auth.mu.Lock()
	auth.revokeErr = errors.New("the row is locked")
	auth.mu.Unlock()

	source.SelectGrant("grant-1")
	outcome, _ := source.executeRevokeGrant(ctx, ActionRequest{
		Action: "CTUI-0665", Target: Target{Kind: "grant", ID: "grant-1"}})

	if outcome.Verdict == VerdictPass {
		t.Fatal("an unrecorded revocation reported success")
	}
	if outcome.Proof.Status.IsSuccess() {
		t.Fatal("an unrecorded revocation carries a successful proof")
	}
}

// Revoking an already-revoked grant is refused at preparation.
func TestRevokingAnAlreadyRevokedGrantIsRefused(t *testing.T) {
	source, auth := testControl(t)
	ctx := context.Background()

	revoked := time.Date(2026, 9, 10, 11, 0, 0, 0, time.UTC)
	auth.mu.Lock()
	binding := auth.grants["grant-1"]
	binding.RevokedAt = &revoked
	auth.grants["grant-1"] = binding
	auth.mu.Unlock()

	source.SelectGrant("grant-1")
	if _, err := source.prepareGrant(ctx); err == nil {
		t.Fatal("an already-revoked grant was offered for revocation")
	}
}
