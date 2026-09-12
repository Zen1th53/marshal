package tui

// Status data: the read-only bindings behind Home and Status.
//
// Every read here answers with a Value, so a screen cannot render a number it
// does not have. That is the whole point of this layer: the canonical sources
// return zero values and nil slices for "nothing", and a zero read as a zero is
// exactly the lie the UX contract forbids. Converting at the boundary means the
// distinction between "no tasks" and "could not ask" is made once, by the code
// that knows which one happened, rather than guessed at by each screen.
//
// This layer owns nothing. It reads canonical state and shapes it for display;
// it never writes, never decides policy, and never holds authority. Status is
// read-only, and that is enforced here by there being nothing to call.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/resources"
	"github.com/Zen1th53/marshal/internal/startup"
)

// RuntimeReader is the canonical runtime state Status displays.
//
// It is an interface rather than *app.Runtime so the TUI depends on the reads
// it makes rather than on the whole runtime, and so the truthful handling of a
// missing runtime is testable without one.
type RuntimeReader interface {
	Status(ctx context.Context) (RuntimeStatus, error)
	Events(ctx context.Context) ([]model.Event, error)
	Tasks(ctx context.Context) ([]model.Task, error)
	InstanceID() string
}

// RuntimeStatus is the canonical status snapshot, restated here so this package
// does not import the runtime for one struct.
type RuntimeStatus struct {
	ProjectID     string
	ProjectName   string
	SchemaVersion int
	AgentCount    int
	SessionCount  int
	TaskCount     int
	LeaseCount    int
}

// AssessmentReader supplies the startup health assessment behind blockers,
// readiness and required actions.
type AssessmentReader interface {
	Assessment(ctx context.Context) (startup.Assessment, error)
}

// ResourceReader supplies the machine resource snapshot.
type ResourceReader interface {
	Resources(ctx context.Context) (resources.Snapshot, error)
}

// CloudReader supplies Community Cloud and ULTRA state.
//
// Every method reports whether the value is evidenced, because the Cloud is the
// surface most likely to be asked about while unconfigured, and an unconfigured
// Cloud must read as UNKNOWN rather than as "not entitled".
type CloudReader interface {
	// Configured reports whether a Cloud is set up at all.
	Configured() bool
	// Entitled reports the canonical gate answer.
	Entitled() bool
	// Capabilities lists the capabilities the verified lease carries.
	Capabilities() []string
	// ExpiresAt returns the lease expiry, if a verified lease is held.
	ExpiresAt() (time.Time, bool)
	// RenewAt returns when renewal is due, if a verified lease is held.
	RenewAt() (time.Time, bool)
	// InstallationID identifies this installation, if one exists.
	InstallationID() string
	// SessionID identifies the Cloud session, if one exists.
	SessionID() string
	// Err is why authorization did not produce ULTRA, if it did not.
	Err() error
}

// StatusSource bundles the readers Status binds to. Any of them may be nil,
// which is why every read checks: a TUI opened without a runtime must still
// render, saying truthfully that it has nothing to read rather than crashing or
// showing zeros.
type StatusSource struct {
	Runtime    RuntimeReader
	Assessment AssessmentReader
	Resources  ResourceReader
	Cloud      CloudReader

	// Now is the clock, injectable so staleness and expiry are testable.
	Now func() time.Time
}

func (s *StatusSource) now() time.Time {
	if s == nil || s.Now == nil {
		return time.Now().UTC()
	}
	return s.Now()
}

// --- runtime ---

// RuntimeSnapshot is the runtime state Status renders, already converted to
// truthful values.
type RuntimeSnapshot struct {
	InstanceID    Value
	ProjectName   Value
	ProjectID     Value
	SchemaVersion Value
	Agents        Value
	Sessions      Value
	Tasks         Value
	Leases        Value
	// Verdict summarises whether the runtime could be read at all.
	Verdict Verdict
}

const runtimeSource = "internal/app/runtime.go Runtime.Status"

// ReadRuntime reads the canonical runtime status.
//
// A missing runtime is UNKNOWN with the reason, not zeros. A user looking at
// "0 tasks" cannot tell whether the project is empty or the daemon is down, and
// those call for opposite responses.
func (s *StatusSource) ReadRuntime(ctx context.Context) RuntimeSnapshot {
	unavailable := func(reason string, status Truth) RuntimeSnapshot {
		v := Value{Status: status, Reason: reason, Source: runtimeSource, Observed: s.now()}
		verdict := VerdictUnknown
		if status == TruthError {
			verdict = VerdictFail
		}
		return RuntimeSnapshot{
			InstanceID: v, ProjectName: v, ProjectID: v, SchemaVersion: v,
			Agents: v, Sessions: v, Tasks: v, Leases: v, Verdict: verdict,
		}
	}

	if s == nil || s.Runtime == nil {
		return unavailable(
			"no runtime is attached to this workspace, so canonical state cannot be read",
			TruthUnknown)
	}

	status, err := s.Runtime.Status(ctx)
	if err != nil {
		return unavailable(
			fmt.Sprintf("the runtime declined to report its status: %s", err), TruthError)
	}

	at := s.now()
	known := func(text string) Value {
		return Value{Text: text, Status: TruthKnown, Source: runtimeSource, Observed: at}
	}
	// A count of zero is a real answer here: the runtime replied. It renders as
	// zero precisely because it was measured, which is the distinction the rest
	// of this file exists to preserve.
	count := func(n int) Value { return known(fmt.Sprintf("%d", n)) }

	snapshot := RuntimeSnapshot{
		SchemaVersion: known(fmt.Sprintf("v%d", status.SchemaVersion)),
		Agents:        count(status.AgentCount),
		Sessions:      count(status.SessionCount),
		Tasks:         count(status.TaskCount),
		Leases:        count(status.LeaseCount),
		Verdict:       VerdictPass,
	}

	if id := s.Runtime.InstanceID(); id != "" {
		snapshot.InstanceID = known(id)
	} else {
		snapshot.InstanceID = Unknown("the runtime reported no instance id", runtimeSource)
	}
	if status.ProjectName != "" {
		snapshot.ProjectName = known(status.ProjectName)
	} else {
		snapshot.ProjectName = Empty(runtimeSource)
	}
	if status.ProjectID != "" {
		snapshot.ProjectID = known(status.ProjectID)
	} else {
		snapshot.ProjectID = Empty(runtimeSource)
	}
	return snapshot
}

// --- events ---

// EventRow is one recent activity entry.
type EventRow struct {
	When  Value
	Type  Value
	Actor Value
	Task  Value
}

// EventFeed is recent activity, with its own truthful status so an empty feed
// is distinguishable from an unreadable one.
type EventFeed struct {
	Rows   []EventRow
	Status Value
}

const eventSource = "internal/app/events.go Runtime.Events"

// ReadEvents reads recent activity, newest first.
//
// limit bounds the read because the event log grows without limit and a
// dashboard must not become slower the longer a project runs.
func (s *StatusSource) ReadEvents(ctx context.Context, limit int) EventFeed {
	if s == nil || s.Runtime == nil {
		return EventFeed{Status: Unknown(
			"no runtime is attached, so recent activity cannot be read", eventSource)}
	}
	events, err := s.Runtime.Events(ctx)
	if err != nil {
		return EventFeed{Status: Errored(
			fmt.Sprintf("the event log could not be read: %s", err), eventSource)}
	}
	if len(events) == 0 {
		return EventFeed{Status: Empty(eventSource)}
	}

	// Newest first. The store's order is not part of its contract, so this
	// sorts rather than assuming.
	sorted := make([]model.Event, len(events))
	copy(sorted, events)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Timestamp.After(sorted[j].Timestamp)
	})
	total := len(sorted)
	if limit > 0 && len(sorted) > limit {
		sorted = sorted[:limit]
	}

	at := s.now()
	rows := make([]EventRow, 0, len(sorted))
	for _, e := range sorted {
		row := EventRow{
			When: Known(relativeTime(at, e.Timestamp), eventSource),
			Type: Known(e.Type, eventSource),
		}
		if e.Timestamp.IsZero() {
			row.When = Unknown("the event carried no timestamp", eventSource)
		}
		if e.Type == "" {
			row.Type = Unknown("the event carried no type", eventSource)
		}
		if e.ActorAgentID != "" {
			row.Actor = Known(e.ActorAgentID, eventSource)
		} else {
			row.Actor = Empty(eventSource)
		}
		if e.TaskID != "" {
			row.Task = Known(e.TaskID, eventSource)
		} else {
			row.Task = Empty(eventSource)
		}
		rows = append(rows, row)
	}
	// The status reports the real total, not the number kept. Saying
	// "12 events" when the log holds hundreds understates the project's history
	// on every screen that shows this summary.
	status := Known(fmt.Sprintf("%d events", total), eventSource)
	if total > len(rows) {
		status = Known(fmt.Sprintf("%d events (showing the %d most recent)",
			total, len(rows)), eventSource)
	}
	return EventFeed{Rows: rows, Status: status}
}

// relativeTime renders how long ago something happened.
//
// A timestamp in the future is reported as such rather than as "0s ago": clock
// skew between a daemon and the UI is real, and silently clamping it hides a
// problem worth seeing.
func relativeTime(now, then time.Time) string {
	d := now.Sub(then)
	if d < 0 {
		return fmt.Sprintf("in %s", roundDuration(-d))
	}
	return fmt.Sprintf("%s ago", roundDuration(d))
}

func roundDuration(d time.Duration) string {
	switch {
	case d < time.Second:
		return "under 1s"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// --- blockers ---

// Blocker is one thing standing between the user and progress.
//
// RemediationOwner names the canonical screen that can fix it, which is how
// Status stays read-only: it says what is wrong and where to go, and the going
// is a cross-link rather than an action.
type Blocker struct {
	ID       Value
	Summary  Value
	Impact   Value
	Remedy   Value
	Required bool
	// Owner is the canonical menu path that can resolve this blocker.
	Owner string
}

// BlockerList is the blockers and required actions, with a truthful status when
// the assessment itself could not be read.
type BlockerList struct {
	Blockers []Blocker
	// Attention holds non-blocking observations worth showing.
	Attention []Blocker
	Status    Value
	// Readiness is the overall verdict.
	Readiness Verdict
}

const assessmentSource = "internal/startup/assessment.go"

// ReadBlockers reads the startup assessment as blockers and required actions.
func (s *StatusSource) ReadBlockers(ctx context.Context) BlockerList {
	if s == nil || s.Assessment == nil {
		return BlockerList{
			Status: NotRun(
				"no readiness assessment has been run for this session", assessmentSource),
			Readiness: VerdictNotRun,
		}
	}
	assessment, err := s.Assessment.Assessment(ctx)
	if err != nil {
		return BlockerList{
			Status: Errored(
				fmt.Sprintf("the readiness assessment could not be read: %s", err),
				assessmentSource),
			Readiness: VerdictUnknown,
		}
	}

	if assessment.AssessedAt.IsZero() && len(assessment.Checks) == 0 {
		return BlockerList{
			Status:    NotRun("readiness has not been assessed yet", assessmentSource),
			Readiness: VerdictNotRun,
		}
	}

	list := BlockerList{
		Blockers:  toBlockers(assessment.Blocking()),
		Attention: toBlockers(assessment.Attention()),
	}
	// An assessment that ran and found nothing blocking is a PASS. An
	// assessment that never ran is NOT_RUN — never PASS.
	switch {
	case len(list.Blockers) > 0:
		list.Readiness = VerdictBlocked
		list.Status = Known(fmt.Sprintf("%d blocking", len(list.Blockers)), assessmentSource)
	case len(list.Attention) > 0:
		list.Readiness = VerdictPass
		list.Status = Known(fmt.Sprintf("%d needing attention", len(list.Attention)), assessmentSource)
	default:
		list.Readiness = VerdictPass
		list.Status = Empty(assessmentSource)
	}
	return list
}

func toBlockers(checks []startup.Check) []Blocker {
	if len(checks) == 0 {
		return nil
	}
	out := make([]Blocker, 0, len(checks))
	for _, c := range checks {
		b := Blocker{
			ID:       Known(c.ID, assessmentSource),
			Required: c.Required,
			Owner:    ownerForCheck(c),
		}
		if c.Summary != "" {
			b.Summary = Known(c.Summary, assessmentSource)
		} else {
			b.Summary = Unknown("the check reported no summary", assessmentSource)
		}
		if c.Impact != "" {
			b.Impact = Known(c.Impact, assessmentSource)
		} else {
			b.Impact = Empty(assessmentSource)
		}
		// A remedy is advice, never an action this screen takes.
		if c.Remedy != "" {
			b.Remedy = Known(c.Remedy, assessmentSource)
		} else {
			b.Remedy = Empty(assessmentSource)
		}
		out = append(out, b)
	}
	return out
}

// ownerForCheck names the canonical screen that can resolve a check.
//
// The mapping is by health dimension rather than by check id, so a new check
// inherits a sensible destination instead of silently getting none. An unmapped
// dimension yields no owner, and the UI then says so rather than offering a
// cross-link that goes nowhere.
func ownerForCheck(c startup.Check) string {
	// The check id is tried first, because it names the specific thing that
	// failed and the frozen IA has a screen for each. Routing the sandbox probe
	// to "Models" because its dimension is "environment" would send a user
	// looking at a broken container to a list of model providers.
	switch c.ID {
	case "env.sandbox":
		return "MARSHAL — COMMUNITY TUI / Security / Sandbox"
	case "env.network":
		return "MARSHAL — COMMUNITY TUI / Security / Network"
	case "env.ultra":
		// ULTRA is requested and changed under Control, not configured here.
		return "MARSHAL — COMMUNITY TUI / Control / Mode & Autonomy"
	}

	// The four dimensions are the canonical health model's own enum, not names
	// invented here: core, environment, project and execution. They are the
	// fallback for a check this build has not been taught by name.
	switch c.Dimension {
	case startup.DimensionCore:
		// Runtime, store and schema health live under System.
		return "MARSHAL — COMMUNITY TUI / System"
	case startup.DimensionEnvironment:
		// Every environment check MARSHAL currently emits is a security
		// prerequisite, so an unrecognised one lands there rather than at a
		// screen that certainly cannot help.
		return "MARSHAL — COMMUNITY TUI / Security"
	case startup.DimensionProject:
		return "MARSHAL — COMMUNITY TUI / Work"
	case startup.DimensionExecution:
		// Whether work may run under policy is a Security question.
		return "MARSHAL — COMMUNITY TUI / Security"
	}
	return ""
}

// --- resources ---

// ResourceSnapshot is the machine state Status renders.
type ResourceSnapshot struct {
	CPU         Value
	Concurrency Value
	Memory      Value
	Swap        Value
	Storage     Value
	GPU         Value
	Ollama      Value
	Overall     Value
	Warnings    []Value
	Status      Value
}

const resourceSource = "internal/resources/resources.go Collector.Collect"

// ReadResources reads the machine resource snapshot.
//
// Collection is partial by nature: a machine with no GPU is not a failure, and
// a collector failure on one dimension must not blank the others. Each field
// therefore carries its own status.
func (s *StatusSource) ReadResources(ctx context.Context) ResourceSnapshot {
	if s == nil || s.Resources == nil {
		v := Unknown("no resource collector is attached to this workspace", resourceSource)
		return ResourceSnapshot{
			CPU: v, Concurrency: v, Memory: v, Swap: v, Storage: v,
			GPU: v, Ollama: v, Overall: v, Status: v,
		}
	}
	snap, err := s.Resources.Resources(ctx)
	if err != nil {
		v := Errored(fmt.Sprintf("resources could not be collected: %s", err), resourceSource)
		return ResourceSnapshot{
			CPU: v, Concurrency: v, Memory: v, Swap: v, Storage: v,
			GPU: v, Ollama: v, Overall: v, Status: v,
		}
	}

	out := ResourceSnapshot{Status: Known("collected", resourceSource)}

	// The model and the core count are two separate observations and either can
	// be missing on its own. Pairing them unconditionally would render
	// "Some CPU (0 logical)", which states a measured zero that was never
	// measured — so each part is only shown when it was actually read.
	switch {
	case snap.CPU.Model != "" && snap.CPU.Logical > 0:
		out.CPU = Known(fmt.Sprintf("%s (%d logical)", snap.CPU.Model, snap.CPU.Logical), resourceSource)
	case snap.CPU.Model != "":
		out.CPU = Known(snap.CPU.Model, resourceSource)
	case snap.CPU.Logical > 0:
		out.CPU = Known(fmt.Sprintf("%d logical", snap.CPU.Logical), resourceSource)
	default:
		out.CPU = Unknown("the CPU could not be identified on this system", resourceSource)
	}

	if snap.CPU.Effective > 0 {
		out.Concurrency = Known(fmt.Sprintf("%d", snap.CPU.Effective), resourceSource)
	} else {
		out.Concurrency = Unknown("no effective concurrency was determined", resourceSource)
	}

	// The total is the observation that says memory was read at all. With a
	// total but no available figure, the total alone is shown rather than
	// "0 B available", which would report exhausted memory on a healthy machine.
	switch {
	case snap.Memory.TotalBytes > 0 && snap.Memory.AvailableBytes > 0:
		out.Memory = Known(fmt.Sprintf("%s available of %s",
			humanBytes(snap.Memory.AvailableBytes), humanBytes(snap.Memory.TotalBytes)), resourceSource)
	case snap.Memory.TotalBytes > 0:
		out.Memory = Known(fmt.Sprintf("%s total; available was not reported",
			humanBytes(snap.Memory.TotalBytes)), resourceSource)
	default:
		out.Memory = Unknown("memory totals were not readable", resourceSource)
	}

	// "No swap configured" is a real answer, but only when memory was read at
	// all. With an unreadable /proc/meminfo every field is zero, and asserting
	// "none configured" from that would turn a failed probe into an
	// observation about the machine.
	switch {
	case snap.Memory.SwapTotalBytes > 0:
		out.Swap = Known(fmt.Sprintf("%s used of %s",
			humanBytes(snap.Memory.SwapUsedBytes), humanBytes(snap.Memory.SwapTotalBytes)), resourceSource)
	case snap.Memory.TotalBytes > 0:
		// Memory was read and reported no swap, so there is none.
		out.Swap = Known("none configured", resourceSource)
	default:
		out.Swap = Unknown("swap could not be read because memory was not readable", resourceSource)
	}

	switch {
	case snap.Storage.TotalBytes > 0 && snap.Storage.FreeBytes > 0:
		out.Storage = Known(fmt.Sprintf("%s free of %s",
			humanBytes(snap.Storage.FreeBytes), humanBytes(snap.Storage.TotalBytes)), resourceSource)
	case snap.Storage.TotalBytes > 0 && snap.Storage.Source != "":
		// The collector recorded where it read this from, so the read
		// succeeded: zero free really means a full disk, which is urgent and
		// must not be softened into "not reported".
		out.Storage = Known(fmt.Sprintf("0 B free of %s — the volume is full",
			humanBytes(snap.Storage.TotalBytes)), resourceSource)
	case snap.Storage.TotalBytes > 0:
		out.Storage = Known(fmt.Sprintf("%s total; free space was not reported",
			humanBytes(snap.Storage.TotalBytes)), resourceSource)
	default:
		out.Storage = Unknown("storage totals were not readable", resourceSource)
	}

	// No accelerator is EMPTY only when the probe actually ran. A collector
	// that recorded a failure did not establish absence, and EMPTY there would
	// assert "this machine has no GPU" on the strength of nvidia-smi timing
	// out.
	if len(snap.Accelerators) == 0 {
		if len(snap.Failures) > 0 {
			out.GPU = Unknown(fmt.Sprintf(
				"accelerator detection did not complete: %s",
				strings.Join(snap.Failures, "; ")), resourceSource)
		} else {
			out.GPU = Empty(resourceSource)
		}
	} else {
		parts := make([]string, 0, len(snap.Accelerators))
		for _, a := range snap.Accelerators {
			label := strings.TrimSpace(a.Vendor + " " + a.Model)
			if label == "" {
				label = "unidentified accelerator"
			}
			if a.TotalVRAMBytes != nil {
				label += fmt.Sprintf(" (%s VRAM)", humanBytes(*a.TotalVRAMBytes))
			}
			parts = append(parts, label)
		}
		out.GPU = Known(strings.Join(parts, ", "), resourceSource)
	}

	switch {
	case snap.Ollama.Status == "":
		out.Ollama = Unknown("Ollama was not probed", resourceSource)
	case strings.EqualFold(snap.Ollama.Status, "unavailable"):
		out.Ollama = Offline("Ollama is not reachable at its endpoint", resourceSource)
	default:
		out.Ollama = Known(fmt.Sprintf("%s (%d models)", snap.Ollama.Status, len(snap.Ollama.Models)), resourceSource)
	}

	if snap.Health.Overall != "" {
		out.Overall = Known(string(snap.Health.Overall), resourceSource)
	} else {
		out.Overall = Unknown("no overall health was assessed", resourceSource)
	}

	for _, warning := range snap.Health.Warnings {
		out.Warnings = append(out.Warnings, Known(warning, resourceSource))
	}
	// A collector that failed on some dimension says so rather than presenting
	// a partial snapshot as complete.
	if len(snap.Failures) > 0 {
		out.Status = Value{
			Text:     "collected with failures",
			Status:   TruthStale,
			Reason:   fmt.Sprintf("%d collection step(s) failed: %s", len(snap.Failures), strings.Join(snap.Failures, "; ")),
			Source:   resourceSource,
			Observed: s.now(),
		}
	}
	return out
}

func humanBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit && exp < 4; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTP"[exp])
}

// --- Community Cloud and ULTRA ---

// CloudSnapshot is the Cloud and ULTRA state Status renders.
type CloudSnapshot struct {
	Mode           Value
	Connection     Value
	Installation   Value
	Session        Value
	Lease          Value
	Capabilities   Value
	Expiry         Value
	Renewal        Value
	Refusal        Value
	StandardFallbk Value
}

const cloudSource = "internal/cloud/gate.go, internal/cloud/state.go"

// ReadCloud reads Community Cloud and ULTRA state.
//
// Nothing here is inferred. An unconfigured Cloud is UNKNOWN rather than "not
// entitled", because those are different facts: one means the user never set it
// up, the other means the server said no. The governance contract forbids the
// TUI from recreating server authority, so every answer comes from the gate.
func (s *StatusSource) ReadCloud(ctx context.Context) CloudSnapshot {
	if s == nil || s.Cloud == nil {
		v := Unknown("no Community Cloud session is attached to this workspace", cloudSource)
		return CloudSnapshot{
			Mode:       Known("Standard", cloudSource),
			Connection: v, Installation: v, Session: v, Lease: v,
			Capabilities: v, Expiry: v, Renewal: v, Refusal: v,
			StandardFallbk: Known("active: Community runs locally without the Cloud", cloudSource),
		}
	}

	c := s.Cloud
	out := CloudSnapshot{}

	if !c.Configured() {
		unconfigured := NotRun(
			"no Community Cloud is configured for this installation", cloudSource)
		return CloudSnapshot{
			Mode:         Known("Standard", cloudSource),
			Connection:   unconfigured,
			Installation: unconfigured,
			Session:      unconfigured,
			Lease:        unconfigured,
			Capabilities: unconfigured,
			Expiry:       unconfigured,
			Renewal:      unconfigured,
			Refusal:      unconfigured,
			StandardFallbk: Known(
				"active: Community runs locally without the Cloud", cloudSource),
		}
	}

	// The gate is the only authority. This reads its answer; it never derives
	// entitlement from a local flag.
	entitled := c.Entitled()
	if entitled {
		out.Mode = Known("ULTRA", cloudSource)
		out.StandardFallbk = Known("not in use: a verified lease is active", cloudSource)
	} else {
		out.Mode = Known("Standard", cloudSource)
		out.StandardFallbk = Known(
			"active: Community runs locally without ULTRA", cloudSource)
	}

	if id := c.InstallationID(); id != "" {
		out.Installation = Known(id, cloudSource)
	} else {
		out.Installation = Unknown("no installation identity has been established", cloudSource)
	}
	if id := c.SessionID(); id != "" {
		out.Session = Known(id, cloudSource)
	} else {
		out.Session = Unknown("no Cloud session has been established", cloudSource)
	}

	// A refusal is a result, and it is the answer to "why is ULTRA not active".
	if err := c.Err(); err != nil {
		out.Refusal = Refused(err.Error(), cloudSource)
		out.Connection = Offline(
			fmt.Sprintf("the Cloud did not authorize this session: %s", err), cloudSource)
	} else if entitled {
		out.Refusal = Empty(cloudSource)
		out.Connection = Known("authorized", cloudSource)
	} else {
		out.Refusal = Empty(cloudSource)
		out.Connection = Unknown(
			"the Cloud is configured but this session holds no verified lease", cloudSource)
	}

	if !entitled {
		// The canonical gate retires a lapsed lease outright, so by the time it
		// answers "not entitled" the expiry it once held is gone. "Expired" and
		// "never held one" are therefore genuinely indistinguishable here, and
		// this must not claim to tell them apart.
		//
		// The one case that can be distinguished is a lease still present but
		// past its window, which the gate reports while it has not yet been
		// asked to re-evaluate.
		if expiry, ok := c.ExpiresAt(); ok {
			lapsed := Value{
				Text:   expiry.UTC().Format(time.RFC3339),
				Status: TruthStale,
				Reason: "the lease window has passed; ULTRA has already dropped to Standard",
				Source: cloudSource, Observed: s.now(),
			}
			out.Lease = Value{
				Status: TruthStale,
				Reason: "the last verified lease has expired and was not renewed",
				Source: cloudSource, Observed: s.now(),
			}
			out.Expiry = lapsed
			out.Capabilities = NotRun(
				"an expired lease grants nothing, so no capability is held", cloudSource)
			if renew, ok := c.RenewAt(); ok {
				out.Renewal = Value{
					Text:   renew.UTC().Format(time.RFC3339),
					Status: TruthStale,
					Reason: "renewal was due before the lease lapsed",
					Source: cloudSource, Observed: s.now(),
				}
			} else {
				out.Renewal = NotRun("no renewal is scheduled without a lease", cloudSource)
			}
			return out
		}

		noLease := NotRun(
			"no verified lease is held by this session; if one was held earlier it has "+
				"already been retired, so this cannot distinguish expiry from never having one",
			cloudSource)
		out.Lease, out.Capabilities, out.Expiry, out.Renewal = noLease, noLease, noLease, noLease
		return out
	}

	out.Lease = Known("verified", cloudSource)

	caps := c.Capabilities()
	if len(caps) == 0 {
		out.Capabilities = Empty(cloudSource)
	} else {
		out.Capabilities = Known(strings.Join(caps, ", "), cloudSource)
	}

	at := s.now()
	if expiry, ok := c.ExpiresAt(); ok {
		remaining := expiry.Sub(at)
		if remaining <= 0 {
			out.Expiry = Value{
				Text: expiry.UTC().Format(time.RFC3339), Status: TruthStale,
				Reason: "the lease window has passed; the gate will refuse until it is renewed",
				Source: cloudSource, Observed: at,
			}
		} else {
			out.Expiry = Known(fmt.Sprintf("%s (in %s)",
				expiry.UTC().Format(time.RFC3339), roundDuration(remaining)), cloudSource)
		}
	} else {
		out.Expiry = Unknown("the lease reported no expiry", cloudSource)
	}

	if renew, ok := c.RenewAt(); ok {
		out.Renewal = Known(fmt.Sprintf("%s (in %s)",
			renew.UTC().Format(time.RFC3339), roundDuration(renew.Sub(at))), cloudSource)
	} else {
		out.Renewal = Unknown("no renewal time was reported", cloudSource)
	}
	return out
}

// --- providers ---

// ProviderRow is one provider's evidenced state.
type ProviderRow struct {
	Name Value
	// Availability is whether the provider's CLI was found.
	Availability Value
	Version      Value
	Model        Value
	// Quota, Reset and Allowance are the three fields the frozen pack marks as
	// implementation gaps. They are never populated by inference.
	Quota     Value
	Reset     Value
	Allowance Value
	Auth      Value
}

// ProviderList is the provider surface, all of it evidence-gated.
type ProviderList struct {
	Rows   []ProviderRow
	Status Value
}

const providerSource = "internal/harness/intelligence.go, internal/adapter/adapter.go"

// ProviderProbe is one evidenced provider observation supplied by canonical
// code. The TUI never probes providers itself.
type ProviderProbe struct {
	Name      string
	Found     bool
	Path      string
	Version   string
	Model     string
	Probed    bool
	ProbeNote string
}

// ProviderReader supplies evidenced provider observations.
type ProviderReader interface {
	Providers(ctx context.Context) ([]ProviderProbe, error)
}

// ReadProviders renders provider state from evidence only.
//
// Quota, reset time and promotional allowance are rendered as implementation
// gaps, not guessed. No provider exposes them through a supported interface,
// and inventing a number here would put a figure on screen that no canonical
// source could ever confirm — which is precisely what the frozen pack marks
// CTUI-0148, CTUI-0149 and CTUI-0151 as gaps to prevent.
func (s *StatusSource) ReadProviders(ctx context.Context, source ProviderReader) ProviderList {
	gap := func(spec, what string) Value {
		return NotRun(fmt.Sprintf(
			"IMPLEMENTATION GAP (%s): no provider exposes %s through a supported interface, so MARSHAL has no evidence for it",
			spec, what), providerSource)
	}
	quota := gap("CTUI-0148", "a machine-readable quota or capacity")
	reset := gap("CTUI-0149", "a machine-readable quota reset time")
	allowance := gap("CTUI-0151", "a machine-readable temporary or promotional allowance")
	auth := gap("CTUI-0144", "an inspectable authentication or session state")

	if source == nil {
		return ProviderList{Status: NotRun(
			"no provider probe has been run for this session", providerSource)}
	}
	probes, err := source.Providers(ctx)
	if err != nil {
		return ProviderList{Status: Errored(
			fmt.Sprintf("providers could not be probed: %s", err), providerSource)}
	}
	if len(probes) == 0 {
		return ProviderList{Status: Empty(providerSource)}
	}

	rows := make([]ProviderRow, 0, len(probes))
	for _, p := range probes {
		row := ProviderRow{
			Name:      Known(p.Name, providerSource),
			Quota:     quota,
			Reset:     reset,
			Allowance: allowance,
			Auth:      auth,
		}
		switch {
		case !p.Probed:
			note := p.ProbeNote
			if note == "" {
				note = "this provider was not probed in this session"
			}
			row.Availability = NotRun(note, providerSource)
			row.Version = NotRun(note, providerSource)
			row.Model = NotRun(note, providerSource)
		case p.Found:
			row.Availability = Known("installed", providerSource)
			if p.Version != "" {
				row.Version = Known(p.Version, providerSource)
			} else {
				row.Version = Unknown(
					"the provider did not report a version", providerSource)
			}
			if p.Model != "" {
				row.Model = Known(p.Model, providerSource)
			} else {
				row.Model = Unknown(
					"no model has been selected for this provider", providerSource)
			}
		default:
			reason := "the provider's command was not found on PATH"
			row.Availability = Known("not installed", providerSource)
			row.Version = NotRun(reason, providerSource)
			row.Model = NotRun(reason, providerSource)
		}
		rows = append(rows, row)
	}
	return ProviderList{Rows: rows, Status: Known(fmt.Sprintf("%d providers", len(rows)), providerSource)}
}
