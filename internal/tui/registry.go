package tui

import (
	"fmt"
	"strings"
)

// AccessMode defines whether a capability is Read, Write, or Execute.
type AccessMode string

const (
	AccessRead    AccessMode = "READ"
	AccessWrite   AccessMode = "WRITE"
	AccessExecute AccessMode = "EXECUTE"
)

// Capability defines a single user-operable Community feature across CLI and TUI.
type Capability struct {
	ID            string     // Unique identifier e.g. "goal.edit"
	Category      string     // e.g. "GOAL", "TEAM", "POLICY"
	Name          string     // Human-readable title
	Description   string     // Purpose and effect
	Access        AccessMode // READ, WRITE, EXECUTE
	CLISurface    string     // CLI command equivalent
	TUISurface    string     // TUI slash command or interactive view
	KeyboardPath  string     // Keyboard shortcut or key sequence
	PalettePath   string     // Ctrl+P searchable phrase
	IsDestructive bool       // Requires explicit confirmation if true
	RequiresDep   string     // Optional external dependency e.g. "agy"
}

// CapabilityRegistry manages the catalog of user-operable MARSHAL capabilities.
type CapabilityRegistry struct {
	capabilities map[string]Capability
	orderedIDs   []string
}

// GlobalRegistry holds the canonical capability catalog.
var GlobalRegistry *CapabilityRegistry

func init() {
	GlobalRegistry = NewCapabilityRegistry()
	registerAllCapabilities(GlobalRegistry)
}

// NewCapabilityRegistry creates an empty registry.
func NewCapabilityRegistry() *CapabilityRegistry {
	return &CapabilityRegistry{
		capabilities: make(map[string]Capability),
	}
}

// Register adds a capability to the registry.
func (r *CapabilityRegistry) Register(cap Capability) {
	if _, exists := r.capabilities[cap.ID]; !exists {
		r.orderedIDs = append(r.orderedIDs, cap.ID)
	}
	r.capabilities[cap.ID] = cap
}

// Get retrieves a capability by ID.
func (r *CapabilityRegistry) Get(id string) (Capability, bool) {
	c, ok := r.capabilities[id]
	return c, ok
}

// All returns all registered capabilities in registration order.
func (r *CapabilityRegistry) All() []Capability {
	var list []Capability
	for _, id := range r.orderedIDs {
		list = append(list, r.capabilities[id])
	}
	return list
}

// Count returns the total number of registered capabilities.
func (r *CapabilityRegistry) Count() int {
	return len(r.capabilities)
}

// ToPaletteActions converts registered capabilities into searchable CommandPalette actions.
func (r *CapabilityRegistry) ToPaletteActions() []PaletteAction {
	var actions []PaletteAction
	for _, cap := range r.All() {
		actions = append(actions, PaletteAction{
			ID:          cap.ID,
			Category:    cap.Category,
			Title:       cap.Name,
			Description: cap.Description,
			Command:     cap.TUISurface,
			Shortcut:    cap.KeyboardPath,
		})
	}
	return actions
}

// ParityAuditReport summarizes the feature-parity status.
type ParityAuditReport struct {
	TotalCapabilities int
	ReadOnlyCount     int
	MutableCount      int
	TUIMappedCount    int
	CLIOnlyRemaining  int
	BlockedByDepCount int
}

// AuditParity verifies that all capabilities are mapped to TUI with zero CLI-only gaps.
func (r *CapabilityRegistry) AuditParity() ParityAuditReport {
	report := ParityAuditReport{
		TotalCapabilities: len(r.capabilities),
	}

	for _, cap := range r.capabilities {
		if cap.Access == AccessRead {
			report.ReadOnlyCount++
		} else {
			report.MutableCount++
		}

		// Mapped means the capability names a TUI surface. It deliberately does
		// not imply the surface was exercised: whether a command actually
		// dispatches is established by TestEveryRegisteredCommandIsDispatched
		// and the PTY conformance suite, not by a non-empty string here.
		if cap.TUISurface != "" {
			report.TUIMappedCount++
		} else {
			if cap.CLISurface != "" {
				report.CLIOnlyRemaining++
			}
		}

		if cap.RequiresDep != "" {
			report.BlockedByDepCount++
		}
	}

	return report
}

// FormatAuditReport outputs a formatted audit string matching user criteria.
func (r *CapabilityRegistry) FormatAuditReport() string {
	rep := r.AuditParity()
	var b strings.Builder
	b.WriteString("=== MARSHAL TUI v2 Capability Parity Audit ===\n")
	b.WriteString(fmt.Sprintf("Total capabilities:            %d\n", rep.TotalCapabilities))
	b.WriteString(fmt.Sprintf("Read-only:                     %d\n", rep.ReadOnlyCount))
	b.WriteString(fmt.Sprintf("Mutable:                       %d\n", rep.MutableCount))
	b.WriteString(fmt.Sprintf("TUI mapped:                    %d / %d\n", rep.TUIMappedCount, rep.TotalCapabilities))
	b.WriteString(fmt.Sprintf("CLI-only remaining:            %d\n", rep.CLIOnlyRemaining))
	b.WriteString(fmt.Sprintf("Blocked by external dependency: %d\n", rep.BlockedByDepCount))
	return b.String()
}

func registerAllCapabilities(r *CapabilityRegistry) {
	r.Register(Capability{ID: "verification.status", Category: "VERIFICATION", Name: "Inspect Process 06 verification", Description: "Read canonical evidence-driven verification state", Access: AccessRead, CLISurface: "marshal review status VERIFICATION-ID", TUISurface: "/review <verification_id>", PalettePath: "verification status"})
	// Process 07 learning and memory. Every capability here is read: promotion,
	// revision and invalidation are runtime-service operations, so no surface
	// offers a print-only mutation.
	// Process 08 governed optimization. Every capability here is read:
	// promotion, canary and rollback are runtime-service operations, so no
	// surface offers a print-only mutation of an optimization decision.
	r.Register(Capability{ID: "optimization.cycle.status", Category: "OPTIMIZATION", Name: "Inspect Process 08 optimization cycle", Description: "Read a canonical optimization cycle with its vetoes and blocked paths", Access: AccessRead, CLISurface: "marshal optimization show OPTIMIZATION-ID", TUISurface: "/optimization <optimization_id>", PalettePath: "optimization cycle"})
	r.Register(Capability{ID: "optimization.candidates", Category: "OPTIMIZATION", Name: "Inspect optimization candidates", Description: "List candidates with their declared effects and cluster identity", Access: AccessRead, CLISurface: "marshal optimization candidates OPTIMIZATION-ID", TUISurface: "/optimization <optimization_id>", PalettePath: "optimization candidates"})
	r.Register(Capability{ID: "optimization.counterfactuals", Category: "OPTIMIZATION", Name: "Inspect counterfactual evaluations", Description: "Read alternate-route comparisons with their stated limitations", Access: AccessRead, CLISurface: "marshal optimization counterfactuals OPTIMIZATION-ID", TUISurface: "/optimization <optimization_id>", PalettePath: "optimization counterfactuals"})
	r.Register(Capability{ID: "optimization.manifests", Category: "OPTIMIZATION", Name: "Inspect benchmark manifests", Description: "Read pinned benchmark provenance, including which runs were official", Access: AccessRead, CLISurface: "marshal optimization manifests OPTIMIZATION-ID", TUISurface: "/optimization <optimization_id>", PalettePath: "optimization manifests"})
	r.Register(Capability{ID: "optimization.canaries", Category: "OPTIMIZATION", Name: "Inspect canary rollouts", Description: "Read bounded rollouts, their triggers and their rollback reasons", Access: AccessRead, CLISurface: "marshal optimization canaries OPTIMIZATION-ID", TUISurface: "/rollback <canary_id>", PalettePath: "optimization canaries"})
	r.Register(Capability{ID: "learning.commit.status", Category: "LEARNING", Name: "Inspect Process 07 memory commit", Description: "Read a canonical memory commit with its promotions and refusals", Access: AccessRead, CLISurface: "marshal learning show MEMORY-COMMIT-ID", TUISurface: "/learning <memory_commit_id>", PalettePath: "learning commit"})
	r.Register(Capability{ID: "learning.memory.search", Category: "LEARNING", Name: "Search durable memory", Description: "Bounded, scope- and freshness-aware memory retrieval with contradiction signals", Access: AccessRead, CLISurface: "marshal learning search --project ID", TUISurface: "/memory-search <project_id>", PalettePath: "learning search"})
	r.Register(Capability{ID: "learning.memory.provenance", Category: "LEARNING", Name: "Inspect memory provenance", Description: "Read one memory item's full immutable version history", Access: AccessRead, CLISurface: "marshal learning history ITEM-ID", TUISurface: "/provenance <item_id>", PalettePath: "learning provenance"})
	r.Register(Capability{ID: "learning.routing.trust", Category: "LEARNING", Name: "Inspect routing trust", Description: "Measured routing outcomes including failures, blocked runs and selection bias", Access: AccessRead, CLISurface: "marshal learning trust TASK-CLASS", TUISurface: "/trust [task-class]", PalettePath: "learning trust"})
	r.Register(Capability{ID: "learning.fingerprints", Category: "LEARNING", Name: "Inspect failure fingerprints", Description: "Bounded, freshness-aware failure fingerprints", Access: AccessRead, CLISurface: "marshal learning fingerprints --project ID", TUISurface: "/fingerprints <project_id>", PalettePath: "learning fingerprints"})
	r.Register(Capability{ID: "learning.playbooks", Category: "LEARNING", Name: "Inspect playbook candidates", Description: "List candidate procedures awaiting review; candidates are never active", Access: AccessRead, CLISurface: "marshal learning playbooks --project ID", TUISurface: "/playbooks <project_id>", PalettePath: "learning playbooks"})
	r.Register(Capability{ID: "learning.replay.index", Category: "LEARNING", Name: "Inspect replay index", Description: "Read reproducibility classes and the side effects that block automatic replay", Access: AccessRead, CLISurface: "marshal learning replays RUN-ID", TUISurface: "/replay-index [run_id]", PalettePath: "learning replay"})
	r.Register(Capability{ID: "learning.benchmarks", Category: "LEARNING", Name: "Inspect benchmark records", Description: "Read reproducible evaluation records pinned to a benchmark version and tree", Access: AccessRead, CLISurface: "marshal learning benchmarks NAME", TUISurface: "/replay-index", PalettePath: "learning benchmarks"})
	// 1. Goal Domain (12)
	r.Register(Capability{
		ID: "goal.create", Category: "GOAL", Name: "Create Goal",
		Description: "Initialize a new goal with objectives and criteria",
		Access:      AccessWrite, CLISurface: "marshal goal create",
		TUISurface: "/goal create", KeyboardPath: "g c", PalettePath: "goal create",
	})
	r.Register(Capability{
		ID: "goal.inspect", Category: "GOAL", Name: "Inspect Goal",
		Description: "View active goal objectives, progress, and criteria",
		Access:      AccessRead, CLISurface: "marshal goal",
		TUISurface: "/goal", KeyboardPath: "g i", PalettePath: "goal inspect",
	})
	r.Register(Capability{
		ID: "goal.edit", Category: "GOAL", Name: "Edit Goal",
		Description: "Modify active goal objective and description",
		Access:      AccessWrite, CLISurface: "marshal goal edit",
		TUISurface: "/goal edit", KeyboardPath: "g e", PalettePath: "goal edit",
	})
	r.Register(Capability{
		ID: "goal.version", Category: "GOAL", Name: "View Goal Version",
		Description: "Inspect goal revision history and version numbers",
		Access:      AccessRead, CLISurface: "marshal goal version",
		TUISurface: "/goal version", KeyboardPath: "g v", PalettePath: "goal version",
	})
	r.Register(Capability{
		ID: "goal.diff", Category: "GOAL", Name: "View Goal Diff",
		Description: "View diff between active goal and previous version",
		Access:      AccessRead, CLISurface: "marshal goal diff",
		TUISurface: "/goal diff", KeyboardPath: "g d", PalettePath: "goal diff",
	})
	r.Register(Capability{
		ID: "goal.success_criteria", Category: "GOAL", Name: "Success Criteria",
		Description: "Inspect verifiable success conditions for goal completion",
		Access:      AccessRead, CLISurface: "marshal goal criteria",
		TUISurface: "/goal criteria", KeyboardPath: "g s", PalettePath: "goal criteria",
	})
	r.Register(Capability{
		ID: "goal.constraints.view", Category: "GOAL", Name: "View Constraints",
		Description: "Inspect immutable and active constraints on goal",
		Access:      AccessRead, CLISurface: "marshal goal constraints",
		TUISurface: "/goal constraints", KeyboardPath: "g c", PalettePath: "goal constraints",
	})
	r.Register(Capability{
		ID: "goal.constraints.add", Category: "GOAL", Name: "Add Constraint",
		Description: "Add a new constraint to prevent undesirable behaviors",
		Access:      AccessWrite, CLISurface: "marshal goal add-constraint",
		TUISurface: "/goal add-constraint", KeyboardPath: "g +", PalettePath: "goal add constraint",
	})
	r.Register(Capability{
		ID: "goal.constraints.remove", Category: "GOAL", Name: "Remove Constraint",
		Description: "Remove a mutable constraint adhering to policy",
		Access:      AccessWrite, CLISurface: "marshal goal rm-constraint",
		TUISurface: "/goal rm-constraint", KeyboardPath: "g -", PalettePath: "goal remove constraint",
	})
	r.Register(Capability{
		ID: "goal.donotdo", Category: "GOAL", Name: "Do-Not-Do Constraints",
		Description: "Inspect explicit negative constraints (anti-goals)",
		Access:      AccessRead, CLISurface: "marshal goal donotdo",
		TUISurface: "/goal donotdo", KeyboardPath: "g n", PalettePath: "goal do not do",
	})
	r.Register(Capability{
		ID: "goal.progress", Category: "GOAL", Name: "Inspect Goal Progress",
		Description: "Inspect percentage and task completion metrics",
		Access:      AccessRead, CLISurface: "marshal goal progress",
		TUISurface: "/goal progress", KeyboardPath: "g p", PalettePath: "goal progress",
	})
	// Each lifecycle verb is registered separately: TUISurface names exactly one
	// command so the palette, completion, and conformance harness can all parse
	// it. A combined "a, b, c" surface parses as a single unknown command.
	r.Register(Capability{
		ID: "goal.pause", Category: "GOAL", Name: "Pause Goal",
		Description: "Pause the active collaborative session",
		Access:      AccessWrite, CLISurface: "marshal pause",
		TUISurface: "/pause", KeyboardPath: "g p", PalettePath: "goal pause",
	})
	r.Register(Capability{
		ID: "goal.resume", Category: "GOAL", Name: "Resume Goal",
		Description: "Resume a paused collaborative session",
		Access:      AccessWrite, CLISurface: "marshal resume",
		TUISurface: "/resume", KeyboardPath: "g r", PalettePath: "goal resume",
	})
	r.Register(Capability{
		ID: "goal.cancel", Category: "GOAL", Name: "Cancel Goal",
		Description: "Cancel active goal execution and record termination",
		Access:      AccessWrite, CLISurface: "marshal cancel",
		TUISurface: "/cancel", KeyboardPath: "g c", PalettePath: "goal cancel",
		IsDestructive: true,
	})

	// 2. Team & Collaboration Domain (10)
	r.Register(Capability{
		ID: "team.list", Category: "TEAM", Name: "List Participants",
		Description: "List all active agents, roles, and status",
		Access:      AccessRead, CLISurface: "marshal agents",
		TUISurface: "/agents", KeyboardPath: "F2", PalettePath: "agents list",
	})
	r.Register(Capability{
		ID: "team.inspect", Category: "TEAM", Name: "Inspect Agent Details",
		Description: "View agent harness, model, task, and cost telemetry",
		Access:      AccessRead, CLISurface: "marshal agents inspect",
		TUISurface: "/agents inspect", KeyboardPath: "a i", PalettePath: "agent inspect",
	})
	r.Register(Capability{
		ID: "team.add", Category: "TEAM", Name: "Add Participant",
		Description: "Add a new agent participant to the team session",
		Access:      AccessWrite, CLISurface: "marshal agents add",
		TUISurface: "/agents add", KeyboardPath: "a +", PalettePath: "agent add participant",
	})
	r.Register(Capability{
		ID: "team.remove", Category: "TEAM", Name: "Remove Participant",
		Description: "Remove an agent participant from the team",
		Access:      AccessWrite, CLISurface: "marshal agents remove",
		TUISurface: "/agents remove", KeyboardPath: "a -", PalettePath: "agent remove",
	})
	r.Register(Capability{
		ID: "team.enable_disable", Category: "TEAM", Name: "Toggle Agent",
		Description: "Enable or disable an agent without removing it",
		Access:      AccessWrite, CLISurface: "marshal agents toggle",
		TUISurface: "/agents toggle", KeyboardPath: "a t", PalettePath: "agent toggle",
	})
	r.Register(Capability{
		ID: "team.role", Category: "TEAM", Name: "Assign Fixed Role",
		Description: "Explicitly assign or change an agent's role",
		Access:      AccessWrite, CLISurface: "marshal agents role",
		TUISurface: "/agents role", KeyboardPath: "a r", PalettePath: "agent assign role",
	})
	r.Register(Capability{
		ID: "team.message", Category: "TEAM", Name: "Message Agent",
		Description: "Send direct instruction to a specific agent",
		Access:      AccessWrite, CLISurface: "marshal msg @agent",
		TUISurface: "@<agent> <message>", KeyboardPath: "@", PalettePath: "message agent",
	})
	r.Register(Capability{
		ID: "team.broadcast", Category: "TEAM", Name: "Broadcast Message",
		Description: "Send broadcast instruction to entire active team",
		Access:      AccessWrite, CLISurface: "marshal msg @team",
		TUISurface: "@team <message>", KeyboardPath: "@team", PalettePath: "message team broadcast",
	})
	r.Register(Capability{
		ID: "team.pause_resume", Category: "TEAM", Name: "Pause/Resume Agent",
		Description: "Pause or resume individual agent execution",
		Access:      AccessWrite, CLISurface: "marshal pause @agent",
		TUISurface: "/pause @<agent>", KeyboardPath: "a p", PalettePath: "agent pause resume",
	})
	r.Register(Capability{
		ID: "team.handoff", Category: "TEAM", Name: "Manage Handoffs",
		Description: "Initiate, inspect, and transition tasks between agents",
		Access:      AccessWrite, CLISurface: "marshal handoff",
		TUISurface: "/handoff", KeyboardPath: "h", PalettePath: "handoff task",
	})

	// 3. Harness & ULTRA Domain (10)
	r.Register(Capability{
		ID: "harness.discover", Category: "HARNESS", Name: "Probe Harnesses",
		Description: "Probe local PATH and APIs for installed harnesses",
		Access:      AccessExecute, CLISurface: "marshal harness probe",
		TUISurface: "/harness probe", KeyboardPath: "h p", PalettePath: "harness probe discover",
	})
	r.Register(Capability{
		ID: "harness.status", Category: "HARNESS", Name: "Harness Status",
		Description: "Inspect discovered versions, paths, and availability",
		Access:      AccessRead, CLISurface: "marshal harness status",
		TUISurface: "/harness status", KeyboardPath: "h s", PalettePath: "harness status",
	})
	r.Register(Capability{
		ID: "harness.select", Category: "HARNESS", Name: "Select Harness",
		Description: "Unavailable until Runtime provides an authenticated execution-profile service",
		Access:      AccessWrite, CLISurface: "marshal harness select",
		TUISurface: "/harness select", KeyboardPath: "h l", PalettePath: "harness select",
	})
	r.Register(Capability{
		ID: "model.select", Category: "HARNESS", Name: "Select Model",
		Description: "Unavailable until Runtime provides an authenticated execution-profile service",
		Access:      AccessWrite, CLISurface: "marshal model select",
		TUISurface: "/model select", KeyboardPath: "m s", PalettePath: "model select",
	})
	r.Register(Capability{
		ID: "native.mode", Category: "HARNESS", Name: "Set Native Mode",
		Description: "Configure native execution mode per harness",
		Access:      AccessWrite, CLISurface: "marshal mode select",
		TUISurface: "/mode select", KeyboardPath: "m m", PalettePath: "mode select native",
	})
	r.Register(Capability{
		ID: "native.effort", Category: "HARNESS", Name: "Set Reasoning Effort",
		Description: "Unavailable until Runtime provides an authenticated execution-profile service",
		Access:      AccessWrite, CLISurface: "marshal effort",
		TUISurface: "/effort", KeyboardPath: "m e", PalettePath: "reasoning effort set",
	})
	r.Register(Capability{
		ID: "policy.tool", Category: "HARNESS", Name: "Configure Tool Policy",
		Description: "Restrict or permit specific tools per harness",
		Access:      AccessWrite, CLISurface: "marshal policy tool",
		TUISurface: "/policy tool", KeyboardPath: "p t", PalettePath: "policy tools configure",
	})
	r.Register(Capability{
		ID: "context.strategy", Category: "HARNESS", Name: "Context Strategy",
		Description: "Configure sliding window, compaction, or full context",
		Access:      AccessWrite, CLISurface: "marshal context strategy",
		TUISurface: "/context strategy", KeyboardPath: "c s", PalettePath: "context strategy configure",
	})
	r.Register(Capability{
		ID: "ultra.toggle", Category: "HARNESS", Name: "ULTRA Entitlement Status",
		Description: "Report that ULTRA is unavailable without a verified entitlement",
		Access:      AccessRead, CLISurface: "marshal ultra",
		TUISurface: "/ultra toggle", KeyboardPath: "u t", PalettePath: "ultra toggle mode",
	})
	r.Register(Capability{
		ID: "routing.inspect", Category: "ROUTING", Name: "Inspect Routing",
		Description: "View routing decisions, scores, and selection basis",
		Access:      AccessRead, CLISurface: "marshal route",
		TUISurface: "/route", KeyboardPath: "F7", PalettePath: "routing inspect route",
	})

	// 4. Claims & Claim Graph Domain (8)
	r.Register(Capability{
		ID: "claim.list", Category: "CLAIMS", Name: "List Claims",
		Description: "List all epistemic claims with states and owners",
		Access:      AccessRead, CLISurface: "marshal claims",
		TUISurface: "/claims", KeyboardPath: "F3", PalettePath: "claims list",
	})
	r.Register(Capability{
		ID: "claim.inspect", Category: "CLAIMS", Name: "Inspect Claim",
		Description: "Detailed inspection of claim statement, support, counter-evidence",
		Access:      AccessRead, CLISurface: "marshal inspect C-...",
		TUISurface: "/inspect C-<id>", KeyboardPath: "c i", PalettePath: "claim inspect",
	})
	r.Register(Capability{
		ID: "claim.graph", Category: "CLAIMS", Name: "View Claim Graph",
		Description: "Visual DAG / tree view of claim dependencies",
		Access:      AccessRead, CLISurface: "marshal claims graph",
		TUISurface: "/claims graph", KeyboardPath: "c g", PalettePath: "claim graph view",
	})
	r.Register(Capability{
		ID: "claim.challenge", Category: "CLAIMS", Name: "Challenge Claim",
		Description: "Contest a claim with reason or counter-evidence",
		Access:      AccessWrite, CLISurface: "marshal claims challenge",
		TUISurface: "/claims challenge", KeyboardPath: "c c", PalettePath: "claim challenge",
	})
	r.Register(Capability{
		ID: "claim.verify", Category: "CLAIMS", Name: "Request Verification",
		Description: "Schedule independent verification for a claim",
		Access:      AccessWrite, CLISurface: "marshal claims verify",
		TUISurface: "/claims verify", KeyboardPath: "c v", PalettePath: "claim verify",
	})
	r.Register(Capability{
		ID: "claim.dependencies", Category: "CLAIMS", Name: "Claim Dependencies",
		Description: "Inspect dependency tree and blockers for a claim",
		Access:      AccessRead, CLISurface: "marshal claims deps",
		TUISurface: "/claims deps", KeyboardPath: "c d", PalettePath: "claim dependencies",
	})
	r.Register(Capability{
		ID: "claim.history", Category: "CLAIMS", Name: "Claim Revision History",
		Description: "Inspect state transitions and history for a claim",
		Access:      AccessRead, CLISurface: "marshal claims history",
		TUISurface: "/claims history", KeyboardPath: "c h", PalettePath: "claim history revisions",
	})
	r.Register(Capability{
		ID: "claim.coverage", Category: "CLAIMS", Name: "Critical Coverage",
		Description: "Inspect missing critical claims and coverage ratio",
		Access:      AccessRead, CLISurface: "marshal claims coverage",
		TUISurface: "/claims coverage", KeyboardPath: "c r", PalettePath: "claim critical coverage",
	})

	// 5. Evidence Domain (8)
	r.Register(Capability{
		ID: "evidence.list", Category: "EVIDENCE", Name: "List Evidence Ledger",
		Description: "Inspect evidence ledger with deterministic/probabilistic tags",
		Access:      AccessRead, CLISurface: "marshal evidence",
		TUISurface: "/evidence", KeyboardPath: "F4", PalettePath: "evidence list",
	})
	r.Register(Capability{
		ID: "evidence.inspect", Category: "EVIDENCE", Name: "Inspect Evidence",
		Description: "View evidence source, provenance, and environment binding",
		Access:      AccessRead, CLISurface: "marshal evidence inspect E-...",
		TUISurface: "/evidence inspect E-<id>", KeyboardPath: "e i", PalettePath: "evidence inspect",
	})
	r.Register(Capability{
		ID: "evidence.filter", Category: "EVIDENCE", Name: "Filter Evidence",
		Description: "Filter evidence by claim, source, or determinism",
		Access:      AccessRead, CLISurface: "marshal evidence filter",
		TUISurface: "/evidence filter", KeyboardPath: "e f", PalettePath: "evidence filter",
	})
	r.Register(Capability{
		ID: "evidence.stale", Category: "EVIDENCE", Name: "Inspect Stale Evidence",
		Description: "View invalidated or stale evidence items",
		Access:      AccessRead, CLISurface: "marshal evidence stale",
		TUISurface: "/evidence stale", KeyboardPath: "e s", PalettePath: "evidence stale",
	})
	r.Register(Capability{
		ID: "evidence.contradiction", Category: "EVIDENCE", Name: "Inspect Contradictions",
		Description: "Inspect contradictory evidence across agents",
		Access:      AccessRead, CLISurface: "marshal evidence contradictions",
		TUISurface: "/evidence contradictions", KeyboardPath: "e c", PalettePath: "evidence contradictions",
	})
	r.Register(Capability{
		ID: "evidence.reverify", Category: "EVIDENCE", Name: "Re-run Verification",
		Description: "Trigger re-execution of deterministic verification tool",
		Access:      AccessWrite, CLISurface: "marshal evidence reverify",
		TUISurface: "/evidence reverify", KeyboardPath: "e r", PalettePath: "evidence reverify",
	})
	r.Register(Capability{
		ID: "evidence.export", Category: "EVIDENCE", Name: "Export Evidence Bundle",
		Description: "Export self-contained evidence bundle for external audit",
		Access:      AccessExecute, CLISurface: "marshal export evidence",
		TUISurface: "/export evidence", KeyboardPath: "e x", PalettePath: "evidence export bundle",
	})
	r.Register(Capability{
		ID: "evidence.bundle_view", Category: "EVIDENCE", Name: "Inspect Evidence Bundle",
		Description: "View evidence bundle metadata and verification digests",
		Access:      AccessRead, CLISurface: "marshal evidence bundle",
		TUISurface: "/evidence bundle", KeyboardPath: "e b", PalettePath: "evidence bundle view",
	})

	// 6. Alignment & Guard Domain (6)
	r.Register(Capability{
		ID: "alignment.scope", Category: "ALIGNMENT", Name: "View Scope Boundary",
		Description: "Inspect allowed paths and operational scope boundaries",
		Access:      AccessRead, CLISurface: "marshal alignment scope",
		TUISurface: "/alignment scope", KeyboardPath: "l s", PalettePath: "alignment scope",
	})
	r.Register(Capability{
		ID: "alignment.violations", Category: "ALIGNMENT", Name: "Inspect Violations",
		Description: "View detected out-of-scope modifications and warnings",
		Access:      AccessRead, CLISurface: "marshal alignment violations",
		TUISurface: "/alignment violations", KeyboardPath: "l v", PalettePath: "alignment violations",
	})
	r.Register(Capability{
		ID: "alignment.blast_radius", Category: "ALIGNMENT", Name: "Blast Radius",
		Description: "Compare predicted vs observed file change blast radius",
		Access:      AccessRead, CLISurface: "marshal alignment blast",
		TUISurface: "/alignment blast", KeyboardPath: "l b", PalettePath: "alignment blast radius",
	})
	r.Register(Capability{
		ID: "alignment.deletion_check", Category: "ALIGNMENT", Name: "Deletion Warnings",
		Description: "Inspect deletion-as-satisfaction anti-pattern warnings",
		Access:      AccessRead, CLISurface: "marshal alignment deletions",
		TUISurface: "/alignment deletions", KeyboardPath: "l d", PalettePath: "alignment deletion warnings",
	})
	r.Register(Capability{
		ID: "alignment.escalate", Category: "ALIGNMENT", Name: "Resolve Escalation",
		Description: "Approve or reject scope expansion request",
		Access:      AccessWrite, CLISurface: "marshal alignment resolve",
		TUISurface: "/alignment resolve", KeyboardPath: "l e", PalettePath: "alignment resolve escalation",
	})
	r.Register(Capability{
		ID: "alignment.guard_status", Category: "ALIGNMENT", Name: "Guard Status",
		Description: "Inspect overall Alignment Guard operational state",
		Access:      AccessRead, CLISurface: "marshal alignment status",
		TUISurface: "/alignment status", KeyboardPath: "l g", PalettePath: "alignment guard status",
	})

	// 7. Checkpoints & Rollback Domain (7)
	r.Register(Capability{
		ID: "checkpoint.list", Category: "CHECKPOINTS", Name: "List Checkpoints",
		Description: "List all created checkpoints, dates, and file counts",
		Access:      AccessRead, CLISurface: "marshal checkpoint list",
		TUISurface: "/checkpoint list", KeyboardPath: "k l", PalettePath: "checkpoints list",
	})
	r.Register(Capability{
		ID: "checkpoint.create", Category: "CHECKPOINTS", Name: "Create Checkpoint",
		Description: "Create a named handoff checkpoint with file & claim snapshot",
		Access:      AccessWrite, CLISurface: "marshal checkpoint create",
		TUISurface: "/checkpoint create <name>", KeyboardPath: "k c", PalettePath: "checkpoint create",
	})
	r.Register(Capability{
		ID: "checkpoint.inspect", Category: "CHECKPOINTS", Name: "Inspect Checkpoint",
		Description: "Inspect files, claims, and goal version bound to checkpoint",
		Access:      AccessRead, CLISurface: "marshal checkpoint inspect CP-...",
		TUISurface: "/checkpoint inspect CP-<id>", KeyboardPath: "k i", PalettePath: "checkpoint inspect",
	})
	r.Register(Capability{
		ID: "checkpoint.diff", Category: "CHECKPOINTS", Name: "Checkpoint Diff",
		Description: "View code diff between current worktree and checkpoint",
		Access:      AccessRead, CLISurface: "marshal checkpoint diff CP-...",
		TUISurface: "/checkpoint diff CP-<id>", KeyboardPath: "k d", PalettePath: "checkpoint diff",
	})
	r.Register(Capability{
		ID: "checkpoint.rollback", Category: "CHECKPOINTS", Name: "Execute Rollback",
		Description: "Unavailable until authenticated runtime restoration is implemented",
		Access:      AccessExecute, CLISurface: "marshal rollback CP-...",
		TUISurface: "/rollback CP-<id>", KeyboardPath: "k r", PalettePath: "rollback checkpoint",
		IsDestructive: true,
	})
	r.Register(Capability{
		ID: "checkpoint.cancel_rollback", Category: "CHECKPOINTS", Name: "Cancel Rollback",
		Description: "Dismiss pending rollback prompt",
		Access:      AccessWrite, CLISurface: "marshal rollback cancel",
		TUISurface: "/rollback cancel", KeyboardPath: "k x", PalettePath: "rollback cancel",
	})
	r.Register(Capability{
		ID: "checkpoint.history", Category: "CHECKPOINTS", Name: "Rollback History",
		Description: "View historical rollback events and reasons",
		Access:      AccessRead, CLISurface: "marshal rollback history",
		TUISurface: "/rollback history", KeyboardPath: "k h", PalettePath: "rollback history",
	})

	// 8. Budget & Termination Domain (6)
	r.Register(Capability{
		ID: "budget.view", Category: "BUDGET", Name: "Inspect Budget",
		Description: "View tokens, cost, wall-clock, and calls consumption",
		Access:      AccessRead, CLISurface: "marshal budget",
		TUISurface: "/budget", KeyboardPath: "F8", PalettePath: "budget inspect",
	})
	r.Register(Capability{
		ID: "budget.set_cost", Category: "BUDGET", Name: "Set Cost Budget",
		Description: "Configure maximum allowable monetary spend ($USD)",
		Access:      AccessWrite, CLISurface: "marshal budget cost <amount>",
		TUISurface: "/budget cost <amount>", KeyboardPath: "b c", PalettePath: "budget set cost",
	})
	r.Register(Capability{
		ID: "budget.set_tokens", Category: "BUDGET", Name: "Set Token Budget",
		Description: "Configure maximum cumulative token limit",
		Access:      AccessWrite, CLISurface: "marshal budget tokens <count>",
		TUISurface: "/budget tokens <count>", KeyboardPath: "b t", PalettePath: "budget set tokens",
	})
	r.Register(Capability{
		ID: "budget.set_time", Category: "BUDGET", Name: "Set Time Budget",
		Description: "Configure maximum wall-clock runtime limit",
		Access:      AccessWrite, CLISurface: "marshal budget time <duration>",
		TUISurface: "/budget time <duration>", KeyboardPath: "b m", PalettePath: "budget set time",
	})
	r.Register(Capability{
		ID: "budget.set_calls", Category: "BUDGET", Name: "Set Calls Budget",
		Description: "Configure maximum model invocations limit",
		Access:      AccessWrite, CLISurface: "marshal budget calls <count>",
		TUISurface: "/budget calls <count>", KeyboardPath: "b l", PalettePath: "budget set calls",
	})
	r.Register(Capability{
		ID: "budget.termination", Category: "BUDGET", Name: "Termination Status",
		Description: "Inspect contract termination state and reason",
		Access:      AccessRead, CLISurface: "marshal termination",
		TUISurface: "/termination", KeyboardPath: "b s", PalettePath: "termination status reason",
	})

	// 9. Approvals Domain (6)
	r.Register(Capability{
		ID: "approval.list", Category: "APPROVALS", Name: "List Approvals",
		Description: "View pending approval requests with risk ratings",
		Access:      AccessRead, CLISurface: "marshal approvals",
		TUISurface: "/approvals", KeyboardPath: "p l", PalettePath: "approvals list",
	})
	r.Register(Capability{
		ID: "approval.inspect", Category: "APPROVALS", Name: "Inspect Approval",
		Description: "View detailed reason, requested paths, and policy rule",
		Access:      AccessRead, CLISurface: "marshal approval inspect <id>",
		TUISurface: "/approval inspect <id>", KeyboardPath: "p i", PalettePath: "approval inspect",
	})
	r.Register(Capability{
		ID: "approval.diff", Category: "APPROVALS", Name: "Approval Diff",
		Description: "Inspect diff associated with an approval request",
		Access:      AccessRead, CLISurface: "marshal approval diff <id>",
		TUISurface: "/approval diff <id>", KeyboardPath: "p d", PalettePath: "approval diff",
	})
	r.Register(Capability{
		ID: "approval.approve", Category: "APPROVALS", Name: "Approve Request",
		Description: "Grant permission for pending action or out-of-scope write",
		Access:      AccessWrite, CLISurface: "marshal approve <id>",
		TUISurface: "/approve <id>", KeyboardPath: "A", PalettePath: "approve request",
	})
	r.Register(Capability{
		ID: "approval.reject", Category: "APPROVALS", Name: "Reject Request",
		Description: "Deny permission for pending action or out-of-scope write",
		Access:      AccessWrite, CLISurface: "marshal reject <id>",
		TUISurface: "/reject <id>", KeyboardPath: "R", PalettePath: "reject request",
	})
	r.Register(Capability{
		ID: "approval.history", Category: "APPROVALS", Name: "Approval History",
		Description: "Inspect past approvals, approvers, and timestamps",
		Access:      AccessRead, CLISurface: "marshal approvals history",
		TUISurface: "/approvals history", KeyboardPath: "p h", PalettePath: "approval history",
	})

	// 10. Tasks & Work Activity Domain (7)
	r.Register(Capability{
		ID: "task.list", Category: "TASKS", Name: "List Tasks",
		Description: "List all active, pending, and completed tasks",
		Access:      AccessRead, CLISurface: "marshal tasks",
		TUISurface: "/tasks", KeyboardPath: "t l", PalettePath: "tasks list",
	})
	r.Register(Capability{
		ID: "task.create", Category: "TASKS", Name: "Create Task",
		Description: "Create a new assigned task with priority",
		Access:      AccessWrite, CLISurface: "marshal task create",
		TUISurface: "/task create", KeyboardPath: "t c", PalettePath: "task create",
	})
	r.Register(Capability{
		ID: "task.inspect", Category: "TASKS", Name: "Inspect Task",
		Description: "View task owner, reviewer, files, and dependencies",
		Access:      AccessRead, CLISurface: "marshal task inspect T-...",
		TUISurface: "/task inspect T-<id>", KeyboardPath: "t i", PalettePath: "task inspect",
	})
	r.Register(Capability{
		ID: "task.assign", Category: "TASKS", Name: "Assign Task",
		Description: "Assign owner and reviewer to a task",
		Access:      AccessWrite, CLISurface: "marshal task assign",
		TUISurface: "/task assign", KeyboardPath: "t a", PalettePath: "task assign",
	})
	// One capability per command. A packed surface such as
	// "/task pause / resume / cancel" parses as a single unknown command, so it
	// advertises three controls while providing none that resolve.
	r.Register(Capability{
		ID: "task.pause", Category: "TASKS", Name: "Pause Task",
		Description: "Pause an individual task",
		Access:      AccessWrite, CLISurface: "marshal task pause",
		TUISurface: "/task pause", KeyboardPath: "t p", PalettePath: "task pause",
	})
	r.Register(Capability{
		ID: "task.resume", Category: "TASKS", Name: "Resume Task",
		Description: "Resume a paused task",
		Access:      AccessWrite, CLISurface: "marshal task resume",
		TUISurface: "/task resume", KeyboardPath: "t u", PalettePath: "task resume",
	})
	r.Register(Capability{
		ID: "task.cancel", Category: "TASKS", Name: "Cancel Task",
		Description: "Cancel an individual task",
		Access:      AccessWrite, CLISurface: "marshal task cancel",
		TUISurface: "/task cancel", KeyboardPath: "t x", PalettePath: "task cancel",
		IsDestructive: true,
	})
	r.Register(Capability{
		ID: "task.retry", Category: "TASKS", Name: "Retry Task",
		Description: "Retry a failed or blocked task with fresh context",
		Access:      AccessWrite, CLISurface: "marshal task retry",
		TUISurface: "/task retry T-<id>", KeyboardPath: "t r", PalettePath: "task retry",
	})
	r.Register(Capability{
		ID: "task.ownership", Category: "TASKS", Name: "Task Ownership",
		Description: "View active work ownership, blockers, and leases",
		Access:      AccessRead, CLISurface: "marshal task ownership",
		TUISurface: "/task ownership", KeyboardPath: "t o", PalettePath: "task ownership",
	})

	// 11. Security, Policy & Sandbox Domain (8)
	r.Register(Capability{
		ID: "policy.network", Category: "POLICY", Name: "Network Policy",
		Description: "Inspect network isolation, egress rules, and fail-closed state",
		Access:      AccessRead, CLISurface: "marshal policy network",
		TUISurface: "/policy network", KeyboardPath: "s n", PalettePath: "network policy egress",
	})
	r.Register(Capability{
		ID: "policy.sandbox", Category: "POLICY", Name: "Sandbox Status",
		Description: "Inspect bubblewrap sandbox backend, jail, and filesystem isolation",
		Access:      AccessRead, CLISurface: "marshal sandbox",
		TUISurface: "/sandbox", KeyboardPath: "s s", PalettePath: "sandbox status bubblewrap",
	})
	r.Register(Capability{
		ID: "policy.capability", Category: "POLICY", Name: "Capability Policy",
		Description: "Inspect process capabilities and system call boundaries",
		Access:      AccessRead, CLISurface: "marshal policy capability",
		TUISurface: "/policy capability", KeyboardPath: "s c", PalettePath: "capability policy",
	})
	r.Register(Capability{
		ID: "policy.scope", Category: "POLICY", Name: "Scope Policy",
		Description: "Inspect filesystem read/write boundary rules",
		Access:      AccessRead, CLISurface: "marshal policy scope",
		TUISurface: "/policy scope", KeyboardPath: "s p", PalettePath: "scope policy",
	})
	r.Register(Capability{
		ID: "policy.write", Category: "POLICY", Name: "Write Permissions",
		Description: "Inspect explicit write permissions and approvals per agent",
		Access:      AccessRead, CLISurface: "marshal policy write",
		TUISurface: "/policy write", KeyboardPath: "s w", PalettePath: "write permissions policy",
	})
	r.Register(Capability{
		ID: "policy.audit", Category: "POLICY", Name: "Security Audit Log",
		Description: "View security audit events, sandbox escapes, and denials",
		Access:      AccessRead, CLISurface: "marshal policy audit",
		TUISurface: "/policy audit", KeyboardPath: "s a", PalettePath: "security audit log",
	})
	r.Register(Capability{
		ID: "provider.status", Category: "PROVIDER", Name: "Provider Status",
		Description: "Inspect provider endpoints, auth tokens, and egress availability",
		Access:      AccessRead, CLISurface: "marshal provider status",
		TUISurface: "/provider status", KeyboardPath: "p s", PalettePath: "provider status egress",
	})
	r.Register(Capability{
		ID: "provider.configure", Category: "PROVIDER", Name: "Configure Provider",
		Description: "Set provider API key securely and configure endpoints",
		Access:      AccessWrite, CLISurface: "marshal provider config",
		TUISurface: "/provider config", KeyboardPath: "p c", PalettePath: "provider configure key",
	})

	// 12. Memory & Epistemic Domain (6)
	r.Register(Capability{
		ID: "memory.inspect", Category: "MEMORY", Name: "Inspect Shared Memory",
		Description: "View epistemic knowledge items and shared team state",
		Access:      AccessRead, CLISurface: "marshal memory",
		TUISurface: "/memory", KeyboardPath: "m i", PalettePath: "memory inspect shared",
	})
	r.Register(Capability{
		ID: "memory.search", Category: "MEMORY", Name: "Search Memory",
		Description: "Full-text search over shared epistemic memories and provenances",
		Access:      AccessRead, CLISurface: "marshal memory search <query>",
		TUISurface: "/memory search <query>", KeyboardPath: "m /", PalettePath: "memory search",
	})
	r.Register(Capability{
		ID: "memory.provenance", Category: "MEMORY", Name: "Memory Provenance",
		Description: "Inspect origin agent, timestamp, and verification for memory",
		Access:      AccessRead, CLISurface: "marshal memory provenance",
		TUISurface: "/memory provenance", KeyboardPath: "m p", PalettePath: "memory provenance",
	})
	r.Register(Capability{
		ID: "blind.status", Category: "EPISTEMIC", Name: "Blind Interpretation",
		Description: "Inspect blind independent interpretations and divergence score",
		Access:      AccessRead, CLISurface: "marshal blind status",
		TUISurface: "/blind status", KeyboardPath: "i s", PalettePath: "blind interpretation status",
	})
	r.Register(Capability{
		ID: "blind.resolve", Category: "EPISTEMIC", Name: "Resolve Ambiguity",
		Description: "Provide user disambiguation decision on divergent interpretations",
		Access:      AccessWrite, CLISurface: "marshal blind resolve",
		TUISurface: "/blind resolve", KeyboardPath: "i r", PalettePath: "blind resolve ambiguity",
	})
	r.Register(Capability{
		ID: "constraints.reinjection", Category: "EPISTEMIC", Name: "Reinjection Digests",
		Description: "Inspect cryptographic constraint digests re-injected on handoff",
		Access:      AccessRead, CLISurface: "marshal reinjection",
		TUISurface: "/reinjection", KeyboardPath: "r d", PalettePath: "reinjection constraint digests",
	})

	// 13. Operations & Diagnostics Domain (6)
	r.Register(Capability{
		ID: "doctor.run", Category: "SYSTEM", Name: "System Diagnostics",
		Description: "Run full doctor suite checking database, harnesses, sandbox, and git",
		Access:      AccessRead, CLISurface: "marshal doctor",
		TUISurface: "/doctor", KeyboardPath: "F1", PalettePath: "doctor diagnostics system",
	})
	r.Register(Capability{
		ID: "runtime.status", Category: "SYSTEM", Name: "Runtime Status",
		Description: "Inspect runtime event loop, subscribers, and goroutine health",
		Access:      AccessRead, CLISurface: "marshal runtime",
		TUISurface: "/runtime", KeyboardPath: "s r", PalettePath: "runtime status",
	})
	r.Register(Capability{
		ID: "store.status", Category: "SYSTEM", Name: "Store Status",
		Description: "Inspect SQLite schema version, migration state, and integrity",
		Access:      AccessRead, CLISurface: "marshal store",
		TUISurface: "/store", KeyboardPath: "s d", PalettePath: "store database schema sqlite",
	})
	r.Register(Capability{
		ID: "backup.create", Category: "OPERATIONS", Name: "Create Backup Snapshot",
		Description: "Create a full backup snapshot of session and database state",
		Access:      AccessExecute, CLISurface: "marshal backup create",
		TUISurface: "/backup create", KeyboardPath: "b k", PalettePath: "backup create snapshot",
	})
	r.Register(Capability{
		ID: "backup.restore", Category: "OPERATIONS", Name: "Restore Backup Snapshot",
		Description: "Restore session and database state from a backup archive",
		Access:      AccessExecute, CLISurface: "marshal backup restore <id>",
		TUISurface: "/backup restore <id>", KeyboardPath: "b r", PalettePath: "backup restore",
		IsDestructive: true,
	})
	r.Register(Capability{
		ID: "fingerprint.inspect", Category: "SYSTEM", Name: "Failure Fingerprint",
		Description: "Inspect failure fingerprints and recurring error patterns",
		Access:      AccessRead, CLISurface: "marshal fingerprint",
		TUISurface: "/fingerprint", KeyboardPath: "f p", PalettePath: "failure fingerprint recurring errors",
	})
}
