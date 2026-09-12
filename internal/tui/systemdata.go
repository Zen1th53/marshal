package tui

// System data is the read-only projection of the local runtime boundary.  It
// deliberately keeps host paths, token plaintext and credentials out of the
// snapshot: System is an inventory, not a back door into the machine.

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/resources"
)

// SystemReader is the canonical state System displays.
type SystemReader interface {
	RuntimeStatus(ctx context.Context) (RuntimeStatus, error)
	RuntimeInstanceID() string
	StoreSchemaVersion(ctx context.Context) (int, error)
	StoreIntegrity(ctx context.Context) error
	ObjectCount(ctx context.Context, table string) (int, error)
	CollectResources(ctx context.Context) (resources.Snapshot, error)
	TokenMetadata(ctx context.Context) ([]auth.TokenRecord, error)
}

// SystemFeed bundles System's canonical readers.
type SystemFeed struct {
	Reader SystemReader
	Now    func() time.Time
}

func (s *SystemFeed) now() time.Time {
	if s == nil || s.Now == nil {
		return time.Now().UTC()
	}
	return s.Now()
}

const systemBinding = "internal/app/runtime.go, internal/store, internal/resources/resources.go"

// SystemSnapshot is everything System displays.  Every observable is a Value;
// a missing service can therefore never accidentally render as a zero or an
// empty string.
type SystemSnapshot struct {
	Runtime        Value
	InstanceID     Value
	SchemaVersion  Value
	StoreIntegrity Value
	Project        Value
	Agents         Value
	Sessions       Value
	Tasks          Value
	Leases         Value
	Findings       Value
	Approvals      Value
	Artifacts      Value
	Events         Value

	CPU            Value
	Memory         Value
	Storage        Value
	GPU            Value
	Ollama         Value
	ResourceHealth Value

	Tokens       []SystemTokenRow
	TokensStatus Value

	// These surfaces have no read API with a live server/session instance.  They
	// remain explicit UNKNOWN/NOT_RUN rather than being inferred from imports or
	// a listener that may belong to another process.
	API         Value
	MCP         Value
	A2A         Value
	Lifecycle   Value
	Telemetry   Value
	Release     Value
	Preferences Value
	ObservedAt  Value
}

// SystemTokenRow contains metadata only.  Digest and plaintext are never
// copied into the snapshot: even a digest is unnecessary for an operator to
// choose a token and is safer kept out of the terminal and clipboard.
type SystemTokenRow struct {
	ID           Value
	Name         Value
	Kind         Value
	Capabilities Value
	Standing     Value
	Created      Value
	Credential   Value
}

// ReadSystem gathers the canonical System snapshot at one instant.
func (s *SystemFeed) ReadSystem(ctx context.Context) SystemSnapshot {
	snap := SystemSnapshot{ObservedAt: Known(s.now().Format(time.RFC3339), systemBinding)}
	if s == nil || s.Reader == nil {
		s.fillUnavailable(&snap, Unknown("no runtime/system reader is attached to this workspace", systemBinding))
		return snap
	}

	status, err := s.Reader.RuntimeStatus(ctx)
	if err != nil {
		v := Errored(fmt.Sprintf("runtime status could not be read: %s", err), systemBinding)
		snap.Runtime, snap.Project, snap.Agents, snap.Sessions, snap.Tasks, snap.Leases = v, v, v, v, v, v
	} else {
		snap.Runtime = Known("running", "internal/app.Runtime.Status")
		snap.Project = knownOrEmpty(status.ProjectID, "internal/app.Runtime.Status")
		snap.Agents = Known(fmt.Sprintf("%d", status.AgentCount), "internal/app.Runtime.Status")
		snap.Sessions = Known(fmt.Sprintf("%d", status.SessionCount), "internal/app.Runtime.Status")
		snap.Tasks = Known(fmt.Sprintf("%d", status.TaskCount), "internal/app.Runtime.Status")
		snap.Leases = Known(fmt.Sprintf("%d", status.LeaseCount), "internal/app.Runtime.Status")
	}

	if id := s.Reader.RuntimeInstanceID(); id == "" {
		snap.InstanceID = Unknown("the attached runtime did not expose an instance ID", "internal/app.Runtime.InstanceID")
	} else {
		snap.InstanceID = Known(id, "internal/app.Runtime.InstanceID")
	}
	if version, err := s.Reader.StoreSchemaVersion(ctx); err != nil {
		snap.SchemaVersion = Errored(fmt.Sprintf("schema version could not be read: %s", err), "internal/store.Store.SchemaVersion")
	} else {
		snap.SchemaVersion = Known(fmt.Sprintf("%d", version), "internal/store.Store.SchemaVersion")
	}
	if err := s.Reader.StoreIntegrity(ctx); err != nil {
		snap.StoreIntegrity = Errored(fmt.Sprintf("store integrity check failed: %s", err), "internal/store.Store.Integrity")
	} else {
		snap.StoreIntegrity = Known("ok", "internal/store.Store.Integrity")
	}

	for _, count := range []struct {
		table string
		out   *Value
	}{
		{"findings", &snap.Findings}, {"approvals", &snap.Approvals},
		{"artifacts", &snap.Artifacts}, {"audit_events", &snap.Events},
	} {
		n, err := s.Reader.ObjectCount(ctx, count.table)
		if err != nil {
			*count.out = Errored(fmt.Sprintf("%s count could not be read: %s", count.table, err), "internal/store.Store.Count")
		} else {
			*count.out = Known(fmt.Sprintf("%d", n), "internal/store.Store.Count")
		}
	}
	s.readResources(ctx, &snap)
	s.readTokens(ctx, &snap)
	snap.API = NotRun("the local Unix-socket server exposes no listener/status read for this workspace", "internal/api.Server")
	snap.MCP = NotRun("the MCP server has no live listener/status authority exposed to the workspace", "internal/mcp.Server")
	snap.A2A = NotRun("the A2A server has no live listener/status authority exposed to the workspace", "internal/a2a.Server")
	snap.Lifecycle = Unknown("lifecycle.Adapter exposes typed operations, not a runtime status snapshot", "internal/lifecycle")
	snap.Telemetry = Unknown("no canonical aggregate telemetry read is exposed to the local TUI", "internal/events")
	snap.Release = NotRun("release and conformance tools have not reported a durable result to this workspace", "tools/release_trust.py")
	snap.Preferences = Known("in-process workspace preferences", "internal/tui.Workspace")
	return snap
}

func (s *SystemFeed) fillUnavailable(snap *SystemSnapshot, v Value) {
	snap.Runtime, snap.InstanceID, snap.SchemaVersion, snap.StoreIntegrity = v, v, v, v
	snap.Project, snap.Agents, snap.Sessions, snap.Tasks, snap.Leases = v, v, v, v, v
	snap.Findings, snap.Approvals, snap.Artifacts, snap.Events = v, v, v, v
	snap.CPU, snap.Memory, snap.Storage, snap.GPU, snap.Ollama, snap.ResourceHealth = v, v, v, v, v, v
	snap.TokensStatus, snap.API, snap.MCP, snap.A2A = v, v, v, v
	snap.Lifecycle, snap.Telemetry, snap.Release, snap.Preferences = v, v, v, v
}

func (s *SystemFeed) readResources(ctx context.Context, snap *SystemSnapshot) {
	r, err := s.Reader.CollectResources(ctx)
	if err != nil {
		v := Errored(fmt.Sprintf("resources could not be collected: %s", err), "internal/resources.Collector.Collect")
		snap.CPU, snap.Memory, snap.Storage, snap.GPU, snap.Ollama, snap.ResourceHealth = v, v, v, v, v, v
		return
	}
	if r.CPU.Logical > 0 {
		snap.CPU = Known(fmt.Sprintf("%d logical (%s)", r.CPU.Logical, r.CPU.Architecture), "internal/resources.Collector.Collect")
	} else {
		snap.CPU = Unknown("collector returned no logical CPU count", "internal/resources.Collector.Collect")
	}
	if r.Memory.TotalBytes > 0 {
		snap.Memory = Known(fmt.Sprintf("%d bytes total", r.Memory.TotalBytes), "internal/resources.Collector.Collect")
	} else {
		snap.Memory = Unknown("collector returned no memory total", "internal/resources.Collector.Collect")
	}
	if r.Storage.TotalBytes > 0 {
		snap.Storage = Known(fmt.Sprintf("%d bytes total", r.Storage.TotalBytes), "internal/resources.Collector.Collect")
	} else {
		snap.Storage = Unknown("collector returned no storage total", "internal/resources.Collector.Collect")
	}
	if len(r.Accelerators) == 0 {
		snap.GPU = Empty("internal/resources.Collector.Collect")
	} else {
		snap.GPU = Known(fmt.Sprintf("%d accelerator(s)", len(r.Accelerators)), "internal/resources.Collector.Collect")
	}
	snap.Ollama = knownOrEmpty(r.Ollama.Status, "internal/resources.Collector.Collect")
	snap.ResourceHealth = knownOrEmpty(string(r.Health.Overall), "internal/resources.Collector.Collect")
}

func (s *SystemFeed) readTokens(ctx context.Context, snap *SystemSnapshot) {
	tokens, err := s.Reader.TokenMetadata(ctx)
	if err != nil {
		snap.TokensStatus = Errored(fmt.Sprintf("token metadata could not be read: %s", err), "internal/auth.Manager.ListTokens")
		return
	}
	if len(tokens) == 0 {
		snap.TokensStatus = Empty("internal/auth.Manager.ListTokens")
		return
	}
	sort.SliceStable(tokens, func(i, j int) bool { return tokens[i].ID < tokens[j].ID })
	for _, token := range tokens {
		standing := Known("active", "internal/auth.Manager.ListTokens")
		if token.Revoked {
			standing = Known("revoked", "internal/auth.Manager.ListTokens")
		}
		created := Unknown("the token record has no creation timestamp", "internal/auth.Manager.ListTokens")
		if !token.CreatedAt.IsZero() {
			created = Known(token.CreatedAt.UTC().Format(time.RFC3339), "internal/auth.Manager.ListTokens")
		}
		snap.Tokens = append(snap.Tokens, SystemTokenRow{
			ID:           knownOrEmpty(token.ID, "internal/auth.Manager.ListTokens"),
			Name:         knownOrEmpty(token.Name, "internal/auth.Manager.ListTokens"),
			Kind:         knownOrEmpty(string(token.Kind), "internal/auth.Manager.ListTokens"),
			Capabilities: Known(fmt.Sprintf("%d scoped capability/capabilities", len(token.Capabilities)), "internal/auth.Manager.ListTokens"),
			Standing:     standing,
			Created:      created,
			// Token plaintext and digests are credential material.  Rendering a
			// redacted Value makes the fact that it exists visible while CopyText
			// refuses to move any credential data out of MARSHAL.
			Credential: Value{Status: TruthKnown, Source: "internal/auth.Manager.ListTokens", Observed: s.now(), Redacted: true},
		})
	}
	snap.TokensStatus = Known(fmt.Sprintf("%d token record(s)", len(tokens)), "internal/auth.Manager.ListTokens")
}
