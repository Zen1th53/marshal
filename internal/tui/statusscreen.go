package tui

// Screens: what each frozen IA node renders.
//
// A screen is a pure function from a snapshot to lines. It performs no reads of
// its own — the snapshot was gathered once, so every field on a screen was
// observed at the same moment and the display cannot show two different
// instants side by side.
//
// Screens are keyed by frozen spec id. A node with no screen renders its
// binding honestly rather than a blank pane, so an unimplemented screen is
// visibly unimplemented instead of looking like a screen with no data.

import (
	"fmt"
	"sort"
	"strings"
)

// Snapshot is everything Home and Status display, read at one instant.
type Snapshot struct {
	Runtime   RuntimeSnapshot
	Events    EventFeed
	Blockers  BlockerList
	Resources ResourceSnapshot
	Cloud     CloudSnapshot
	Providers ProviderList
}

// ScreenContent is what a detail pane shows.
type ScreenContent struct {
	// Fields are label/value rows.
	Fields []Field
	// Notice is a screen-level truthful state, shown above the fields when the
	// screen as a whole has something to say — an implementation gap, an
	// unreadable source, a stale snapshot.
	Notice Value
	// HasNotice distinguishes an empty notice from an absent one.
	HasNotice bool
	// CrossLinks are corrective destinations, never actions. Status is
	// read-only: it points at the screen that owns the fix.
	CrossLinks []CrossLinkTarget
	// ReadOnly marks the screen as incapable of mutation, which every Status
	// screen is.
	ReadOnly bool
}

// CrossLinkTarget is a canonical destination offered from a read-only screen.
type CrossLinkTarget struct {
	Label string
	// MenuPath is the canonical owner, resolved through the IA at follow time.
	MenuPath string
	// Reason says why this destination is offered.
	Reason string
}

// RenderNode returns what a frozen IA node displays.
//
// The frozen IA decides which screens exist; this decides what they show. A
// node with no renderer falls through to a truthful description of its binding
// rather than an empty pane.
func RenderNode(n *Node, snap Snapshot) ScreenContent {
	if n == nil {
		return ScreenContent{ReadOnly: true}
	}

	// An implementation gap says so before anything else. A gap screen must
	// never render an empty table that reads as "nothing to report".
	if notice, ok := GapNotice(n); ok {
		content := ScreenContent{Notice: notice, HasNotice: true, ReadOnly: true}
		content.Fields = gapFields(n, snap)
		return content
	}

	if render, ok := screenRenderers[n.SpecID]; ok {
		content := render(snap)
		content.ReadOnly = isReadOnly(n)
		if isBlockerStatusLeaf(n.SpecID) && len(n.Children) == 0 && len(content.Fields) > 0 {
			content.Fields = append([]Field{{Label: n.Title, Value: content.Fields[0].Value}}, content.Fields...)
		}
		if value, exact := exactHomeStatusValue(n.SpecID, snap); exact && len(n.Children) == 0 {
			content.Fields = []Field{{Label: n.Title, Value: value}}
		}
		content = dedicatedLeafContent(n, content, "canonical Home/Status snapshot")
		return content
	}

	return ScreenContent{
		Notice: NotRun(fmt.Sprintf(
			"this screen (%s) is declared by the frozen pack but is not implemented in this build",
			n.SpecID), n.SpecID),
		HasNotice: true,
		ReadOnly:  isReadOnly(n),
	}
}

func isBlockerStatusLeaf(specID string) bool {
	switch specID {
	case "CTUI-0098", "CTUI-0117", "CTUI-0118", "CTUI-0119", "CTUI-0120", "CTUI-0121", "CTUI-0122", "CTUI-0123", "CTUI-0124", "CTUI-0179", "CTUI-0180", "CTUI-0182", "CTUI-0183", "CTUI-0184":
		return true
	default:
		return false
	}
}

// exactHomeStatusValue selects the canonical observation that answers a leaf's
// frozen subject. Group renderers remain useful for their parent screens, but
// a leaf must not make the user infer its answer from a neighbouring summary.
func exactHomeStatusValue(specID string, s Snapshot) (Value, bool) {
	switch specID {
	case "CTUI-0020", "CTUI-0021", "CTUI-0022":
		return s.Events.Status, true
	case "CTUI-0026":
		return s.Blockers.Status, true
	case "CTUI-0027":
		return Known("available from the keyboard help overlay", "internal/tui workspace help"), true
	case "CTUI-0097":
		return s.Blockers.Status, true
	case "CTUI-0101", "CTUI-0102", "CTUI-0103", "CTUI-0106", "CTUI-0107", "CTUI-0108", "CTUI-0109":
		return s.Events.Status, true
	case "CTUI-0113", "CTUI-0114", "CTUI-0130", "CTUI-0134", "CTUI-0169":
		return s.Runtime.Sessions, true
	case "CTUI-0132":
		return s.Runtime.Leases, true
	case "CTUI-0137":
		return providerAvailability(s.Providers, "Claude"), true
	case "CTUI-0138":
		return providerAvailability(s.Providers, "Codex"), true
	case "CTUI-0139":
		return providerAvailability(s.Providers, "Gemini"), true
	case "CTUI-0140":
		return providerAvailability(s.Providers, "OpenCode"), true
	case "CTUI-0141":
		return providerAvailability(s.Providers, "Antigravity"), true
	case "CTUI-0143", "CTUI-0145", "CTUI-0146", "CTUI-0147", "CTUI-0150", "CTUI-0152":
		return s.Providers.Status, true
	case "CTUI-0160":
		return s.Cloud.Expiry, true
	case "CTUI-0161":
		return s.Cloud.Renewal, true
	case "CTUI-0165":
		return s.Runtime.InstanceID, true
	case "CTUI-0170":
		return s.Runtime.InstanceID, true
	}
	return Value{}, false
}

func providerAvailability(list ProviderList, name string) Value {
	for _, row := range list.Rows {
		if strings.EqualFold(row.Name.Text, name) {
			return row.Availability
		}
	}
	if list.Status.Status != TruthUnset {
		return list.Status
	}
	return NotRun("provider discovery did not return this harness", providerSource)
}

// isReadOnly reports whether a node can mutate anything. Every node under
// Status is read-only by contract, and so is every non-action node anywhere.
func isReadOnly(n *Node) bool {
	if !n.Type.IsAction() {
		return true
	}
	// An action anywhere under Status would violate the contract, so it is
	// still reported read-only here and the test suite fails on its existence.
	// The section is found by walking the tree rather than by matching a path
	// string, so renaming a menu path cannot silently disable this.
	return sectionTitleOf(n) == "Status"
}

// sectionTitleOf names the top-level section a node belongs to.
func sectionTitleOf(n *Node) string {
	for cur := n; cur != nil; cur = cur.Parent() {
		if cur.Type == NodeSection {
			return cur.Title
		}
	}
	return ""
}

// gapFields shows whatever a gap screen legitimately does know.
//
// A gap means one specific fact has no binding, not that the whole screen is
// blank. Showing the evidenced neighbours around the gap is more honest than
// hiding them, provided the gap itself stays marked.
func gapFields(n *Node, snap Snapshot) []Field {
	switch n.SpecID {
	case "CTUI-0148", "CTUI-0149", "CTUI-0151", "CTUI-0144":
		// The provider gaps: show which providers exist, with the gapped
		// column marked on each row rather than omitted.
		return providerGapFields(n.SpecID, snap.Providers)
	case "CTUI-0157":
		// Entitlement request state has no canonical store; the surrounding
		// Cloud state is real and is shown.
		return []Field{
			{Label: "Cloud connection", Value: snap.Cloud.Connection},
			{Label: "Current mode", Value: snap.Cloud.Mode},
			{Label: "Refusal reason", Value: snap.Cloud.Refusal},
		}
	}
	return nil
}

func providerGapFields(spec string, list ProviderList) []Field {
	if len(list.Rows) == 0 {
		return []Field{{Label: "Providers", Value: list.Status}}
	}
	fields := make([]Field, 0, len(list.Rows)*2)
	for _, row := range list.Rows {
		var v Value
		switch spec {
		case "CTUI-0148":
			v = row.Quota
		case "CTUI-0149":
			v = row.Reset
		case "CTUI-0151":
			v = row.Allowance
		default:
			v = row.Auth
		}
		fields = append(fields,
			Field{Label: row.Name.Display(), Value: v},
			// The evidenced neighbour. It says which providers this gap is
			// about: a gap listed against a provider that is not even
			// installed means something different from one against a provider
			// in active use.
			Field{Label: "  installed", Value: row.Availability})
	}
	return fields
}

// screenRenderers maps a frozen spec id to what it renders.
var screenRenderers = map[string]func(Snapshot) ScreenContent{

	// --- Home: summary and navigation only ---
	//
	// Home summarises and links. It renders no field the owning section does
	// not already own, and it offers no action of its own.

	"CTUI-0001": func(s Snapshot) ScreenContent { // Home
		return ScreenContent{
			Fields: []Field{
				{Label: "Readiness", Value: readinessValue(s.Blockers)},
				{Label: "Project", Value: s.Runtime.ProjectName},
				{Label: "Tasks", Value: s.Runtime.Tasks},
				{Label: "Blockers", Value: s.Blockers.Status},
				{Label: "Mode", Value: s.Cloud.Mode},
				{Label: "Machine", Value: s.Resources.Overall},
			},
			Notice:    homeNotice(s),
			HasNotice: true,
		}
	},

	"CTUI-0002": func(s Snapshot) ScreenContent { // Home / Dashboard
		return ScreenContent{
			Fields: []Field{
				{Label: "Readiness", Value: readinessValue(s.Blockers)},
				{Label: "Project", Value: s.Runtime.ProjectName},
				{Label: "Schema", Value: s.Runtime.SchemaVersion},
				{Label: "Sessions", Value: s.Runtime.Sessions},
				{Label: "Tasks", Value: s.Runtime.Tasks},
				{Label: "Agents", Value: s.Runtime.Agents},
				{Label: "Blockers", Value: s.Blockers.Status},
				{Label: "Mode", Value: s.Cloud.Mode},
				{Label: "Cloud", Value: s.Cloud.Connection},
				{Label: "Machine", Value: s.Resources.Overall},
				{Label: "Recent activity", Value: s.Events.Status},
			},
		}
	},

	"CTUI-0017": func(s Snapshot) ScreenContent { // Home / Recent Activity
		return ScreenContent{Fields: eventFields(s.Events, 8)}
	},
	"CTUI-0018": func(s Snapshot) ScreenContent { // Task and tool activity
		return ScreenContent{Fields: eventFields(s.Events, 12)}
	},

	// --- Status / Overview ---

	"CTUI-0089": func(s Snapshot) ScreenContent { // Status
		return ScreenContent{
			Fields: []Field{
				{Label: "Readiness", Value: readinessValue(s.Blockers)},
				{Label: "Blockers", Value: s.Blockers.Status},
				{Label: "Runtime", Value: s.Runtime.InstanceID},
				{Label: "Mode", Value: s.Cloud.Mode},
				{Label: "Machine", Value: s.Resources.Overall},
			},
		}
	},

	"CTUI-0090": func(s Snapshot) ScreenContent { // Status / Overview
		return ScreenContent{
			Fields: []Field{
				{Label: "Readiness verdict", Value: readinessValue(s.Blockers)},
				{Label: "Project", Value: s.Runtime.ProjectName},
				{Label: "Project id", Value: s.Runtime.ProjectID},
				{Label: "Sessions", Value: s.Runtime.Sessions},
				{Label: "Tasks", Value: s.Runtime.Tasks},
				{Label: "Blockers", Value: s.Blockers.Status},
				{Label: "Mode", Value: s.Cloud.Mode},
			},
		}
	},

	"CTUI-0091": func(s Snapshot) ScreenContent { // Readiness verdict
		content := ScreenContent{
			Fields: []Field{
				{Label: "Verdict", Value: readinessValue(s.Blockers)},
				{Label: "Assessment", Value: s.Blockers.Status},
			},
		}
		content.CrossLinks = blockerCrossLinks(s.Blockers)
		return content
	},

	"CTUI-0093": func(s Snapshot) ScreenContent { // Active project and session
		return ScreenContent{
			Fields: []Field{
				{Label: "Project", Value: s.Runtime.ProjectName},
				{Label: "Project id", Value: s.Runtime.ProjectID},
				{Label: "Sessions", Value: s.Runtime.Sessions},
				{Label: "Agents", Value: s.Runtime.Agents},
			},
			CrossLinks: []CrossLinkTarget{{
				Label:    "Open Work / Projects & Setup",
				MenuPath: "MARSHAL — COMMUNITY TUI / Work / Projects & Setup",
				Reason:   "the project and session are owned by Work",
			}},
		}
	},

	"CTUI-0095": func(s Snapshot) ScreenContent { // Blockers and required user action
		return blockerScreen(s.Blockers)
	},

	// --- Status / Blockers & Required Actions ---

	"CTUI-0116": func(s Snapshot) ScreenContent { return blockerScreen(s.Blockers) },

	"CTUI-0117": func(s Snapshot) ScreenContent { // Approval required
		return filteredBlockerScreen(s.Blockers, "approval")
	},
	"CTUI-0119": func(s Snapshot) ScreenContent { // Policy or constitutional refusal
		return filteredBlockerScreen(s.Blockers, "policy", "constitution", "refus")
	},
	"CTUI-0121": func(s Snapshot) ScreenContent { // Provider unavailable or rate-limited
		return filteredBlockerScreen(s.Blockers, "provider", "rate", "harness")
	},

	// --- Status / Providers & Usage ---

	"CTUI-0135": func(s Snapshot) ScreenContent { // Providers & Usage
		return providerScreen(s.Providers)
	},
	"CTUI-0142": func(s Snapshot) ScreenContent { // Provider Status
		return providerScreen(s.Providers)
	},
	"CTUI-0143": func(s Snapshot) ScreenContent { // Availability, path, version
		fields := make([]Field, 0, len(s.Providers.Rows)*2)
		if len(s.Providers.Rows) == 0 {
			return ScreenContent{Fields: []Field{{Label: "Providers", Value: s.Providers.Status}}}
		}
		for _, r := range s.Providers.Rows {
			fields = append(fields,
				Field{Label: r.Name.Display(), Value: r.Availability},
				Field{Label: "  version", Value: r.Version})
		}
		return ScreenContent{Fields: fields}
	},
	"CTUI-0152": func(s Snapshot) ScreenContent { // UNKNOWN / NOT_RUN with reason when not evidenced
		// This screen exists to state the policy it enforces, so it shows the
		// unevidenced fields together with why each is unevidenced.
		if len(s.Providers.Rows) == 0 {
			return ScreenContent{Fields: []Field{{Label: "Providers", Value: s.Providers.Status}}}
		}
		fields := make([]Field, 0, len(s.Providers.Rows)*4)
		for _, r := range s.Providers.Rows {
			fields = append(fields,
				Field{Label: r.Name.Display() + " quota", Value: r.Quota},
				Field{Label: r.Name.Display() + " reset", Value: r.Reset},
				Field{Label: r.Name.Display() + " allowance", Value: r.Allowance},
				Field{Label: r.Name.Display() + " auth", Value: r.Auth})
		}
		return ScreenContent{Fields: fields}
	},

	// --- Status / Community Cloud & ULTRA ---

	"CTUI-0153": func(s Snapshot) ScreenContent { return cloudScreen(s.Cloud) },
	"CTUI-0154": func(s Snapshot) ScreenContent { // Current Standard / ULTRA mode
		return ScreenContent{
			Fields: []Field{
				{Label: "Mode", Value: s.Cloud.Mode},
				{Label: "Standard fallback", Value: s.Cloud.StandardFallbk},
			},
			CrossLinks: []CrossLinkTarget{{
				Label:    "Open Control / Mode & Autonomy",
				MenuPath: "MARSHAL — COMMUNITY TUI / Control / Mode & Autonomy",
				Reason:   "changing mode is a Control action; Status only reports it",
			}},
		}
	},
	"CTUI-0155": func(s Snapshot) ScreenContent { // Cloud connection state
		return ScreenContent{Fields: []Field{
			{Label: "Cloud connection state", Value: s.Cloud.Connection},
			{Label: "Refusal or outage", Value: s.Cloud.Refusal},
		}}
	},
	"CTUI-0156": func(s Snapshot) ScreenContent { // Installation and session identity
		return ScreenContent{Fields: []Field{
			{Label: "Installation", Value: s.Cloud.Installation},
			{Label: "Session", Value: s.Cloud.Session},
		}}
	},
	"CTUI-0158": func(s Snapshot) ScreenContent { // Verified signed lease
		return ScreenContent{Fields: []Field{{Label: "Lease", Value: s.Cloud.Lease}}}
	},
	"CTUI-0159": func(s Snapshot) ScreenContent { // Lease capabilities
		return ScreenContent{Fields: []Field{{Label: "Capabilities", Value: s.Cloud.Capabilities}}}
	},
	"CTUI-0160": func(s Snapshot) ScreenContent { // Expiry and remaining lifetime
		return ScreenContent{Fields: []Field{{Label: "Expires", Value: s.Cloud.Expiry}}}
	},
	"CTUI-0161": func(s Snapshot) ScreenContent { // Renewal and heartbeat
		return ScreenContent{Fields: []Field{{Label: "Renews", Value: s.Cloud.Renewal}}}
	},
	"CTUI-0162": func(s Snapshot) ScreenContent { // Refusal or outage reason
		return ScreenContent{Fields: []Field{{Label: "Reason", Value: s.Cloud.Refusal}}}
	},
	"CTUI-0163": func(s Snapshot) ScreenContent { // Standard fallback state
		return ScreenContent{Fields: []Field{{Label: "Fallback", Value: s.Cloud.StandardFallbk}}}
	},

	// --- Status / Runtime ---

	"CTUI-0164": func(s Snapshot) ScreenContent { return runtimeScreen(s.Runtime) },
	"CTUI-0166": func(s Snapshot) ScreenContent { // Runtime health and instance ID
		return ScreenContent{Fields: []Field{
			{Label: "Instance", Value: s.Runtime.InstanceID},
			{Label: "Schema", Value: s.Runtime.SchemaVersion},
		}}
	},
	"CTUI-0167": func(s Snapshot) ScreenContent { // Store and schema health
		return ScreenContent{Fields: []Field{
			{Label: "Schema version", Value: s.Runtime.SchemaVersion},
			{Label: "Project", Value: s.Runtime.ProjectName},
		}}
	},
	"CTUI-0168": func(s Snapshot) ScreenContent { // counts
		return ScreenContent{Fields: []Field{
			{Label: "Tasks", Value: s.Runtime.Tasks},
			{Label: "Agents", Value: s.Runtime.Agents},
			{Label: "Sessions", Value: s.Runtime.Sessions},
			{Label: "Leases", Value: s.Runtime.Leases},
		}}
	},

	// --- Status / Resources ---

	"CTUI-0171": func(s Snapshot) ScreenContent { return resourceScreen(s.Resources) },
	"CTUI-0172": func(s Snapshot) ScreenContent { // CPU and effective concurrency
		return ScreenContent{Fields: []Field{
			{Label: "CPU", Value: s.Resources.CPU},
			{Label: "Effective concurrency", Value: s.Resources.Concurrency},
		}}
	},
	"CTUI-0173": func(s Snapshot) ScreenContent { // RAM and swap
		return ScreenContent{Fields: []Field{
			{Label: "Memory", Value: s.Resources.Memory},
			{Label: "Swap", Value: s.Resources.Swap},
		}}
	},
	"CTUI-0174": func(s Snapshot) ScreenContent { // Storage
		return ScreenContent{Fields: []Field{{Label: "Storage", Value: s.Resources.Storage}}}
	},
	"CTUI-0175": func(s Snapshot) ScreenContent { // GPU inventory
		return ScreenContent{Fields: []Field{{Label: "Accelerators", Value: s.Resources.GPU}}}
	},
	"CTUI-0176": func(s Snapshot) ScreenContent { // Ollama
		return ScreenContent{Fields: []Field{{Label: "Ollama", Value: s.Resources.Ollama}}}
	},
	"CTUI-0177": func(s Snapshot) ScreenContent { // Resource warnings
		if len(s.Resources.Warnings) == 0 {
			return ScreenContent{Fields: []Field{
				{Label: "Warnings", Value: Empty(resourceSource)},
				{Label: "Overall", Value: s.Resources.Overall},
			}}
		}
		fields := make([]Field, 0, len(s.Resources.Warnings)+1)
		fields = append(fields, Field{Label: "Overall", Value: s.Resources.Overall})
		for i, w := range s.Resources.Warnings {
			fields = append(fields, Field{Label: fmt.Sprintf("Warning %d", i+1), Value: w})
		}
		return ScreenContent{Fields: fields}
	},
}

func init() {
	// Explicit frozen screens that share canonical snapshots. Actions and
	// cross-links are intentionally absent: they are rendered by the parent
	// action bar or navigate to their canonical owner.
	bindStatusScreens(homeContinueScreen, "CTUI-0012")
	bindStatusScreens(homeActivityScreen, "CTUI-0019", "CTUI-0020", "CTUI-0021", "CTUI-0022")
	bindStatusScreens(homeHelpScreen, "CTUI-0023", "CTUI-0024", "CTUI-0025", "CTUI-0026", "CTUI-0027", "CTUI-0028")

	bindStatusScreens(statusOverviewDetail, "CTUI-0092", "CTUI-0094", "CTUI-0096", "CTUI-0097", "CTUI-0098")
	bindStatusScreens(lifecycleStatusScreen, "CTUI-0099", "CTUI-0100", "CTUI-0101", "CTUI-0102", "CTUI-0103", "CTUI-0104", "CTUI-0105", "CTUI-0106", "CTUI-0107", "CTUI-0108", "CTUI-0109")
	bindStatusScreens(sessionStatusScreen, "CTUI-0110", "CTUI-0111", "CTUI-0112", "CTUI-0113", "CTUI-0114", "CTUI-0115")
	bindStatusScreens(blockerScreenAll, "CTUI-0118", "CTUI-0120", "CTUI-0122", "CTUI-0123", "CTUI-0124")
	bindStatusScreens(schedulingStatusScreen, "CTUI-0126", "CTUI-0127", "CTUI-0128", "CTUI-0129", "CTUI-0130", "CTUI-0131", "CTUI-0132", "CTUI-0133", "CTUI-0134")
	bindStatusScreens(providerScreenAll, "CTUI-0136", "CTUI-0137", "CTUI-0138", "CTUI-0139", "CTUI-0140", "CTUI-0141", "CTUI-0145", "CTUI-0146", "CTUI-0147", "CTUI-0150")
	bindStatusScreens(runtimeScreenAll, "CTUI-0165", "CTUI-0169", "CTUI-0170")
	bindStatusScreens(alertScreen, "CTUI-0178", "CTUI-0179", "CTUI-0180", "CTUI-0181", "CTUI-0182", "CTUI-0183", "CTUI-0184")
	bindStatusScreens(versionScreen, "CTUI-0185", "CTUI-0186", "CTUI-0187", "CTUI-0188", "CTUI-0189", "CTUI-0190", "CTUI-0191")
}

func bindStatusScreens(render func(Snapshot) ScreenContent, ids ...string) {
	for _, id := range ids {
		screenRenderers[id] = render
	}
}

func homeContinueScreen(s Snapshot) ScreenContent {
	return ScreenContent{ReadOnly: true, Fields: []Field{{"Project", s.Runtime.ProjectName}, {"Sessions", s.Runtime.Sessions}, {"Tasks", s.Runtime.Tasks}, {"Blockers", s.Blockers.Status}}}
}
func homeActivityScreen(s Snapshot) ScreenContent {
	return ScreenContent{ReadOnly: true, Fields: eventFields(s.Events, 12)}
}
func homeHelpScreen(s Snapshot) ScreenContent {
	return ScreenContent{ReadOnly: true, Fields: []Field{{"Navigate", Known("arrows or j/k; Enter opens; Esc returns", "internal/tui")}, {"Focus", Known("Tab / Shift+Tab", "internal/tui")}, {"Command palette", Known("Ctrl+K", "internal/tui")}, {"Context help", Known("?", "internal/tui")}, {"Power-user commands", Known("slash commands remain secondary shortcuts", "internal/tui")}}}
}
func statusOverviewDetail(s Snapshot) ScreenContent {
	return ScreenContent{ReadOnly: true, Fields: []Field{{"Readiness", readinessValue(s.Blockers)}, {"Project", s.Runtime.ProjectName}, {"Sessions", s.Runtime.Sessions}, {"Tasks", s.Runtime.Tasks}, {"Agents", s.Runtime.Agents}, {"Blockers", s.Blockers.Status}, {"Mode", s.Cloud.Mode}, {"Activity", s.Events.Status}}}
}
func lifecycleStatusScreen(s Snapshot) ScreenContent {
	return ScreenContent{ReadOnly: true, Fields: []Field{{"Project", s.Runtime.ProjectName}, {"Sessions", s.Runtime.Sessions}, {"Tasks", s.Runtime.Tasks}, {"Agents/team", s.Runtime.Agents}, {"Leases", s.Runtime.Leases}, {"Lifecycle evidence", Unknown("the Status runtime reader does not expose typed Process 00→08 stage records", runtimeSource)}}}
}
func sessionStatusScreen(s Snapshot) ScreenContent {
	return ScreenContent{ReadOnly: true, Fields: []Field{{"Sessions", s.Runtime.Sessions}, {"Runtime", s.Runtime.InstanceID}, {"Activity", s.Events.Status}, {"Recovery/blockers", s.Blockers.Status}}}
}
func blockerScreenAll(s Snapshot) ScreenContent { return blockerScreen(s.Blockers) }
func schedulingStatusScreen(s Snapshot) ScreenContent {
	return ScreenContent{ReadOnly: true, Fields: []Field{{"Tasks", s.Runtime.Tasks}, {"Agents", s.Runtime.Agents}, {"Durable leases", s.Runtime.Leases}, {"Scheduler detail", Unknown("the Status reader exposes durable counts but no selected scheduler/lease record", runtimeSource)}}}
}
func providerScreenAll(s Snapshot) ScreenContent { return providerScreen(s.Providers) }
func runtimeScreenAll(s Snapshot) ScreenContent  { return runtimeScreen(s.Runtime) }
func alertScreen(s Snapshot) ScreenContent {
	content := blockerScreen(s.Blockers)
	content.Fields = append(content.Fields, eventFields(s.Events, 12)...)
	return content
}
func versionScreen(s Snapshot) ScreenContent {
	return ScreenContent{ReadOnly: true, Fields: []Field{{"Runtime instance", s.Runtime.InstanceID}, {"Schema version", s.Runtime.SchemaVersion}, {"Build/pack/constitution compatibility", Unknown("the Status reader does not expose a combined build, pack and constitution version record", runtimeSource)}}}
}

// --- shared screen builders ---

func homeNotice(s Snapshot) Value {
	// Home summarises; the notice tells the user the single most important
	// thing, which is a blocker if one exists.
	if len(s.Blockers.Blockers) > 0 {
		return Blocked(
			fmt.Sprintf("%d blocker(s) require action before work can proceed",
				len(s.Blockers.Blockers)),
			"MARSHAL — COMMUNITY TUI / Status / Blockers & Required Actions",
			assessmentSource)
	}
	if !s.Blockers.Status.Status.IsSuccess() && s.Blockers.Status.Status != TruthEmpty {
		return s.Blockers.Status
	}
	return Known("no blockers are recorded", assessmentSource)
}

func readinessValue(list BlockerList) Value {
	switch list.Readiness {
	case VerdictPass:
		return Known("READY", assessmentSource)
	case VerdictBlocked:
		return Blocked(
			fmt.Sprintf("%d blocking check(s)", len(list.Blockers)),
			"MARSHAL — COMMUNITY TUI / Status / Blockers & Required Actions",
			assessmentSource)
	case VerdictNotRun:
		// Never PASS by default. An unassessed project is not a ready one.
		return NotRun("readiness has not been assessed in this session", assessmentSource)
	case VerdictFail:
		return Errored("the readiness assessment failed", assessmentSource)
	}
	return Unknown("readiness could not be determined", assessmentSource)
}

func blockerScreen(list BlockerList) ScreenContent {
	content := ScreenContent{ReadOnly: true}
	if len(list.Blockers) == 0 && len(list.Attention) == 0 {
		content.Fields = []Field{
			{Label: "Blockers", Value: list.Status},
			{Label: "Readiness", Value: readinessValue(list)},
		}
		return content
	}
	for _, b := range list.Blockers {
		content.Fields = append(content.Fields, blockerFields(b, "BLOCKING")...)
	}
	for _, b := range list.Attention {
		content.Fields = append(content.Fields, blockerFields(b, "attention")...)
	}
	content.CrossLinks = blockerCrossLinks(list)
	return content
}

func filteredBlockerScreen(list BlockerList, keywords ...string) ScreenContent {
	matched := BlockerList{Status: list.Status, Readiness: list.Readiness}
	for _, b := range list.Blockers {
		if blockerMatches(b, keywords) {
			matched.Blockers = append(matched.Blockers, b)
		}
	}
	for _, b := range list.Attention {
		if blockerMatches(b, keywords) {
			matched.Attention = append(matched.Attention, b)
		}
	}
	if len(matched.Blockers) == 0 && len(matched.Attention) == 0 {
		total := len(list.Blockers) + len(list.Attention)
		content := ScreenContent{ReadOnly: true}
		switch {
		case !list.Status.Status.IsSuccess() && list.Status.Status != TruthEmpty:
			// The assessment itself could not be read, so nothing can be said
			// about this category. Reporting EMPTY here would assert an
			// absence that was never observed.
			content.Fields = []Field{
				{Label: "Matching blockers", Value: list.Status},
				{Label: "Overall readiness", Value: readinessValue(list)},
			}
		case total == 0:
			// The assessment ran and found nothing at all, so there is
			// genuinely nothing of this kind either.
			content.Fields = []Field{
				{Label: "Matching blockers", Value: Empty(assessmentSource)},
				{Label: "Overall readiness", Value: readinessValue(list)},
			}
		default:
			// Blockers exist but none matched this category. The categories are
			// recognised by wording, and canonical checks carry no category of
			// their own — so "none of this kind" is a guess, not an
			// observation, and this says so rather than asserting EMPTY.
			content.Fields = []Field{
				{Label: "Matching blockers", Value: Unknown(fmt.Sprintf(
					"none of the %d recorded blocker(s) were recognised as this kind; "+
						"canonical checks do not carry a category, so this screen "+
						"cannot confirm there are none", total),
					assessmentSource)},
				{Label: "Overall readiness", Value: readinessValue(list)},
			}
			// The full list is one screen away, so a blocker this filter did
			// not recognise is still reachable.
			content.CrossLinks = []CrossLinkTarget{{
				Label:    "Open all Blockers & Required Actions",
				MenuPath: "MARSHAL — COMMUNITY TUI / Status / Blockers & Required Actions",
				Reason:   "shows every recorded blocker, including any this screen did not recognise",
			}}
		}
		return content
	}
	return blockerScreen(matched)
}

func blockerMatches(b Blocker, keywords []string) bool {
	hay := strings.ToLower(b.ID.Display() + " " + b.Summary.Display() + " " + b.Impact.Display())
	for _, k := range keywords {
		if strings.Contains(hay, k) {
			return true
		}
	}
	return false
}

func blockerFields(b Blocker, kind string) []Field {
	label := b.ID.Display()
	if kind == "BLOCKING" {
		label = "! " + label
	}
	fields := []Field{{Label: label, Value: b.Summary}}
	if b.Impact.Status != TruthEmpty {
		fields = append(fields, Field{Label: "  impact", Value: b.Impact})
	}
	if b.Remedy.Status != TruthEmpty {
		fields = append(fields, Field{Label: "  remedy", Value: b.Remedy})
	}
	// A blocker with no known owner is stated as such. Saying nothing would
	// leave the user with a problem, no destination, and no clue that MARSHAL
	// does not know where to send them.
	if b.Owner == "" {
		fields = append(fields, Field{Label: "  where to fix", Value: Unknown(
			"MARSHAL has no mapping from this check to a screen that owns it",
			assessmentSource)})
	}
	return fields
}

// blockerCrossLinks offers the canonical screens that can resolve the blockers.
//
// These are cross-links, not actions: Status reports, and the owning section
// acts. Duplicates are collapsed so a run of blockers in one dimension offers
// one destination rather than five identical ones.
func blockerCrossLinks(list BlockerList) []CrossLinkTarget {
	seen := map[string]string{}
	for _, b := range append(append([]Blocker{}, list.Blockers...), list.Attention...) {
		if b.Owner == "" {
			continue
		}
		if _, ok := seen[b.Owner]; !ok {
			seen[b.Owner] = b.ID.Display()
		}
	}
	if len(seen) == 0 {
		return nil
	}
	owners := make([]string, 0, len(seen))
	for owner := range seen {
		owners = append(owners, owner)
	}
	sort.Strings(owners)

	links := make([]CrossLinkTarget, 0, len(owners))
	for _, owner := range owners {
		short := owner
		if i := strings.LastIndex(owner, " / "); i >= 0 {
			short = owner[i+3:]
		}
		links = append(links, CrossLinkTarget{
			Label:    "Open " + short,
			MenuPath: owner,
			Reason:   fmt.Sprintf("owns the remediation for %s", seen[owner]),
		})
	}
	return links
}

func eventFields(feed EventFeed, limit int) []Field {
	if len(feed.Rows) == 0 {
		return []Field{{Label: "Recent activity", Value: feed.Status}}
	}
	rows := feed.Rows
	truncated := 0
	if limit > 0 && len(rows) > limit {
		truncated = len(rows) - limit
		rows = rows[:limit]
	}
	fields := make([]Field, 0, len(rows)+1)
	for _, r := range rows {
		detail := r.Type.Display()
		if r.Actor.Status == TruthKnown {
			detail += " by " + r.Actor.Display()
		}
		if r.Task.Status == TruthKnown {
			detail += " on " + r.Task.Display()
		}
		fields = append(fields, Field{
			Label: r.When.Display(),
			Value: Value{Text: detail, Status: r.Type.Status, Reason: r.Type.Reason, Source: eventSource},
		})
	}
	// A shortened list must say it was shortened. Without this the newest few
	// events read as the complete recent activity, and a user scanning for
	// something that happened earlier concludes it never did.
	if truncated > 0 {
		fields = append(fields, Field{
			Label: "…",
			Value: Known(fmt.Sprintf(
				"%d more recent event(s) not shown here", truncated), eventSource),
		})
	}
	return fields
}

func providerScreen(list ProviderList) ScreenContent {
	if len(list.Rows) == 0 {
		return ScreenContent{
			ReadOnly: true,
			Fields:   []Field{{Label: "Providers", Value: list.Status}},
		}
	}
	fields := make([]Field, 0, len(list.Rows))
	for _, r := range list.Rows {
		fields = append(fields, Field{Label: r.Name.Display(), Value: r.Availability})
	}
	return ScreenContent{
		ReadOnly: true,
		Fields:   fields,
		CrossLinks: []CrossLinkTarget{{
			Label:    "Open Models / Providers & Harnesses",
			MenuPath: "MARSHAL — COMMUNITY TUI / Models / Providers & Harnesses",
			Reason:   "provider configuration is owned by Models",
		}},
	}
}

func cloudScreen(c CloudSnapshot) ScreenContent {
	return ScreenContent{
		ReadOnly: true,
		Fields: []Field{
			{Label: "Mode", Value: c.Mode},
			{Label: "Connection", Value: c.Connection},
			{Label: "Installation", Value: c.Installation},
			{Label: "Session", Value: c.Session},
			{Label: "Lease", Value: c.Lease},
			{Label: "Capabilities", Value: c.Capabilities},
			{Label: "Expires", Value: c.Expiry},
			{Label: "Renews", Value: c.Renewal},
			{Label: "Refusal", Value: c.Refusal},
			{Label: "Standard fallback", Value: c.StandardFallbk},
		},
		CrossLinks: []CrossLinkTarget{{
			Label:    "Open Control / Mode & Autonomy",
			MenuPath: "MARSHAL — COMMUNITY TUI / Control / Mode & Autonomy",
			Reason:   "requesting or changing ULTRA is a Control action",
		}},
	}
}

func runtimeScreen(r RuntimeSnapshot) ScreenContent {
	return ScreenContent{
		ReadOnly: true,
		Fields: []Field{
			{Label: "Instance", Value: r.InstanceID},
			{Label: "Project", Value: r.ProjectName},
			{Label: "Schema", Value: r.SchemaVersion},
			{Label: "Tasks", Value: r.Tasks},
			{Label: "Agents", Value: r.Agents},
			{Label: "Sessions", Value: r.Sessions},
			{Label: "Leases", Value: r.Leases},
		},
	}
}

func resourceScreen(r ResourceSnapshot) ScreenContent {
	content := ScreenContent{
		ReadOnly: true,
		Fields: []Field{
			{Label: "Overall", Value: r.Overall},
			{Label: "CPU", Value: r.CPU},
			{Label: "Concurrency", Value: r.Concurrency},
			{Label: "Memory", Value: r.Memory},
			{Label: "Swap", Value: r.Swap},
			{Label: "Storage", Value: r.Storage},
			{Label: "Accelerators", Value: r.GPU},
			{Label: "Ollama", Value: r.Ollama},
		},
	}
	if r.Status.Status == TruthStale {
		content.Notice, content.HasNotice = r.Status, true
	}
	return content
}
