package tui

// Security data: the read bindings behind the Security section.
//
// Security is the section where an optimistic default is most dangerous. A
// policy screen that renders "no violations" because it could not read the
// policy engine tells a user they are safe when nothing was checked; a
// capability list that renders empty because a query failed says nobody has
// access when everybody might.
//
// So every read here fails loudly. There is no branch in this file that turns
// a missing answer into a reassuring one, and secrets are never rendered at
// all — only the fact that a lease exists.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/authz"
)

// SecurityReader is the canonical security state the section displays.
type SecurityReader interface {
	// ConstitutionVersion reports the constitution this build enforces.
	ConstitutionVersion(ctx context.Context) (string, error)
	// RoleBindings lists the active capability and role grants.
	RoleBindings(ctx context.Context) ([]authz.RoleBinding, error)
	// SandboxState reports whether isolation is enforceable here.
	SandboxState(ctx context.Context) (SandboxState, error)
	// SecretLeases counts the secret leases held, without their content.
	SecretLeases(ctx context.Context) (int, error)
}

// SandboxState is what the sandbox can actually enforce on this machine.
type SandboxState struct {
	// Available reports whether an isolation backend was found.
	Available bool
	// Backend names it, when one exists.
	Backend string
	// Reason explains an unavailable sandbox in the user's terms.
	Reason string
	// NetworkEnforced reports whether egress can be typed and enforced.
	NetworkEnforced bool
}

// SecurityFeed bundles the readers Security binds to.
type SecurityFeed struct {
	Reader    SecurityReader
	ProjectID string
	Now       func() time.Time
}

func (s *SecurityFeed) now() time.Time {
	if s == nil || s.Now == nil {
		return time.Now().UTC()
	}
	return s.Now()
}

const securityBinding = "internal/constitution, internal/authz, internal/sandbox, internal/secrets"

// SecuritySnapshot is everything Security displays, read at one instant.
type SecuritySnapshot struct {
	Constitution   Value
	Policy         Value
	RiskGates      Value
	TrustedContent Value
	Audit          Value

	Grants       []GrantRow
	GrantsStatus Value

	Sandbox Value
	Network Value

	// Secrets are counted, never listed and never shown.
	SecretLeases Value

	ObservedAt time.Time
}

// GrantRow is one capability or role binding.
type GrantRow struct {
	ID        Value
	Principal Value
	Role      Value
	Scope     Value
	Standing  Value
}

// ReadSecurity gathers security state at one instant.
func (s *SecurityFeed) ReadSecurity(ctx context.Context) SecuritySnapshot {
	snap := SecuritySnapshot{ObservedAt: s.now()}
	if s == nil || s.Reader == nil {
		// Every field says it could not be read. None of them says "fine".
		unavailable := Unknown(
			"no security services are attached to this workspace, so nothing "+
				"here has been checked", securityBinding)
		snap.Constitution, snap.GrantsStatus = unavailable, unavailable
		snap.Policy, snap.RiskGates, snap.TrustedContent, snap.Audit = unavailable, unavailable, unavailable, unavailable
		snap.Sandbox, snap.Network, snap.SecretLeases =
			unavailable, unavailable, unavailable
		return snap
	}

	if version, err := s.Reader.ConstitutionVersion(ctx); err != nil {
		snap.Constitution = Errored(
			fmt.Sprintf("the constitution version could not be read: %s", err),
			securityBinding)
	} else {
		snap.Constitution = knownOrUnknown(version,
			"this build reports no constitution version", securityBinding)
	}

	s.readGrants(ctx, &snap)
	s.readSandbox(ctx, &snap)
	s.readSecrets(ctx, &snap)
	snap.Policy = Unknown("the active policy engine exposes decisions at execution time but no complete policy snapshot through this reader", securityBinding)
	snap.RiskGates = Unknown("no current governed action is selected for a canonical risk-gate decision", securityBinding)
	snap.TrustedContent = Unknown("no ingested trusted-content segment is selected", securityBinding)
	snap.Audit = Unknown("no aggregate security-audit query is exposed by the attached reader", securityBinding)
	return snap
}

func (s *SecurityFeed) readGrants(ctx context.Context, snap *SecuritySnapshot) {
	bindings, err := s.Reader.RoleBindings(ctx)
	switch {
	case err != nil:
		// An unreadable grant list is not an empty one. Rendering it as empty
		// would say nobody has access when the truth is that nobody looked.
		snap.GrantsStatus = Errored(
			fmt.Sprintf("capability and role grants could not be read: %s; "+
				"who holds access is unknown, not none", err), securityBinding)
		return
	case len(bindings) == 0:
		snap.GrantsStatus = Empty(securityBinding)
		return
	}

	sorted := make([]authz.RoleBinding, len(bindings))
	copy(sorted, bindings)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })

	at := s.now()
	active := 0
	for _, binding := range sorted {
		row := GrantRow{
			ID:        Known(binding.ID, securityBinding),
			Principal: knownOrEmpty(binding.PrincipalID, securityBinding),
			Role:      knownOrEmpty(string(binding.Role), securityBinding),
			Scope:     knownOrEmpty(binding.ScopeID, securityBinding),
		}
		row.Standing = grantStanding(binding, at)
		if row.Standing.Status.IsSuccess() {
			active++
		}
		snap.Grants = append(snap.Grants, row)
	}
	snap.GrantsStatus = Known(fmt.Sprintf("%d grants, %d active", len(sorted), active),
		securityBinding)
}

// grantStanding reports whether a grant is still in force.
//
// A revoked or expired grant is shown rather than hidden: knowing that access
// was held and withdrawn is part of the audit trail.
func grantStanding(binding authz.RoleBinding, _ time.Time) Value {
	if binding.RevokedAt != nil && !binding.RevokedAt.IsZero() {
		return Value{
			Text:   "revoked",
			Status: TruthStale,
			Reason: fmt.Sprintf("withdrawn at %s",
				binding.RevokedAt.UTC().Format(time.RFC3339)),
			Source: securityBinding,
		}
	}
	// The binding carries its own canonical state, which is the authority on
	// whether it is in force. Inferring that from timestamps would be a second
	// opinion about who holds access.
	state := strings.ToUpper(strings.TrimSpace(string(binding.State)))
	switch state {
	case "", "ACTIVE":
		return Known("active", securityBinding)
	case "REVOKED", "EXPIRED", "SUSPENDED":
		return Value{
			Text:   strings.ToLower(state),
			Status: TruthStale,
			Reason: "this grant is no longer in force",
			Source: securityBinding,
		}
	}
	// An unrecognised state must not read as active: the safe reading of "I do
	// not know whether this grant is in force" is not "it is".
	return Unknown(fmt.Sprintf(
		"this grant reports state %q, which this build does not recognise; "+
			"whether it is in force is unknown", binding.State), securityBinding)
}

func (s *SecurityFeed) readSandbox(ctx context.Context, snap *SecuritySnapshot) {
	state, err := s.Reader.SandboxState(ctx)
	if err != nil {
		// An unknown sandbox is not a working one. The contract requires
		// unavailable enforcement to fail closed, and the display says so.
		unknown := Unknown(fmt.Sprintf(
			"the sandbox could not be probed: %s; isolation is unverified and "+
				"execution must be treated as unprotected", err), securityBinding)
		snap.Sandbox, snap.Network = unknown, unknown
		return
	}

	if state.Available {
		snap.Sandbox = Known(knownOrDefault(state.Backend, "an isolation backend"),
			securityBinding)
	} else {
		reason := state.Reason
		if reason == "" {
			reason = "no isolation backend was found on this machine"
		}
		// Not a failure to read — a real answer, and a serious one.
		snap.Sandbox = Blocked(reason,
			"MARSHAL — COMMUNITY TUI / Security / Sandbox", securityBinding)
	}

	if state.NetworkEnforced {
		snap.Network = Known("egress decisions are typed and enforced", securityBinding)
	} else {
		snap.Network = Blocked(
			"egress cannot be typed and enforced here, so network access fails "+
				"closed rather than being allowed unchecked",
			"MARSHAL — COMMUNITY TUI / Security / Network", securityBinding)
	}
}

func (s *SecurityFeed) readSecrets(ctx context.Context, snap *SecuritySnapshot) {
	count, err := s.Reader.SecretLeases(ctx)
	if err != nil {
		snap.SecretLeases = Unknown(
			fmt.Sprintf("secret leases could not be counted: %s", err), securityBinding)
		return
	}
	// Only the count. A lease's content stays on the execution host, and this
	// screen never has it to leak in the first place.
	snap.SecretLeases = Value{
		Text:   fmt.Sprintf("%d", count),
		Status: TruthKnown,
		Reason: "leases are counted, never listed: secret material stays on the " +
			"execution host and is not readable from this screen",
		Source: securityBinding,
	}
}

func knownOrDefault(text, fallback string) string {
	if strings.TrimSpace(text) == "" {
		return fallback
	}
	return text
}
