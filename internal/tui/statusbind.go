package tui

// Bindings from the workspace's canonical handles to the read-only interfaces
// Status displays.
//
// These adapters exist so statusdata.go depends on the reads it makes rather
// than on the runtime, the store and the cloud package all at once. They add no
// behaviour: each one forwards to canonical code and converts the result. Where
// a canonical source is absent, the adapter is absent too, and the reader's nil
// handling produces UNKNOWN with a reason rather than a zero.

import (
	"context"
	"time"

	"github.com/Zen1th53/marshal/internal/cloud"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/resources"
	"github.com/Zen1th53/marshal/internal/store"
)

// storeRuntimeReader reads runtime status straight from the canonical store.
//
// The workspace holds a *store.Store rather than an *app.Runtime, and the
// runtime's own Status method is a thin read over exactly these store calls, so
// this reads the same rows the runtime would without constructing a second
// runtime or duplicating its ownership.
type storeRuntimeReader struct {
	store     *store.Store
	projectID string
	sessionID string
}

// runtimeReader returns a reader over the workspace's store, or nil when there
// is no store — in which case Status reports UNKNOWN with the reason.
func (w *Workspace) runtimeReader() RuntimeReader {
	if w.store == nil {
		return nil
	}
	return storeRuntimeReader{store: w.store, projectID: w.projectID, sessionID: w.sessionID}
}

func (r storeRuntimeReader) Status(ctx context.Context) (RuntimeStatus, error) {
	project, err := r.store.Project(ctx)
	if err != nil {
		return RuntimeStatus{}, err
	}
	version, err := r.store.SchemaVersion(ctx)
	if err != nil {
		return RuntimeStatus{}, err
	}
	// A project identifies itself by repository; there is no separate display
	// name in canonical state, and inventing one here would put a label on
	// screen that no source could confirm.
	status := RuntimeStatus{
		ProjectID:     project.ID,
		ProjectName:   project.Repository,
		SchemaVersion: version,
	}
	// Each count is a separate read, and a failure on any of them fails the
	// whole status: a partially counted snapshot would render some real numbers
	// beside some zeros, and nothing on screen would say which was which.
	for _, count := range []struct {
		table string
		into  *int
	}{
		{"agents", &status.AgentCount},
		{"sessions", &status.SessionCount},
		{"tasks", &status.TaskCount},
		{"leases", &status.LeaseCount},
	} {
		n, err := r.store.Count(ctx, count.table)
		if err != nil {
			return RuntimeStatus{}, err
		}
		*count.into = n
	}
	return status, nil
}

func (r storeRuntimeReader) Events(ctx context.Context) ([]model.Event, error) {
	return r.store.ListEvents(ctx)
}

func (r storeRuntimeReader) Tasks(ctx context.Context) ([]model.Task, error) {
	return r.store.ListTasks(ctx)
}

// InstanceID reports the runtime instance id, which a store-only workspace
// does not have.
//
// A runtime instance id names a running daemon process. This workspace reads
// the store directly and there is no such process, so returning the TUI's own
// session id would put a plausible-looking identifier under a label reading
// "runtime instance" and let a user conclude a daemon is up. Returning nothing
// makes the reader render UNKNOWN with the reason instead.
func (r storeRuntimeReader) InstanceID() string { return "" }

// SessionID is what this workspace actually is, reported under its own name.
func (r storeRuntimeReader) SessionID() string { return r.sessionID }

// defaultResourceReader collects machine resources through the canonical
// collector.
type defaultResourceReader struct{}

func (defaultResourceReader) Resources(ctx context.Context) (resources.Snapshot, error) {
	// Collect is bounded internally and reports its own partial failures in the
	// snapshot, so a missing GPU or an absent Ollama is data rather than error.
	return resources.NewCollector().Collect(ctx, ""), nil
}

// workspaceCloudReader answers Cloud questions from the canonical gate.
//
// Every answer comes from the gate. Nothing here recreates server authority:
// there is deliberately no way for this type to report entitlement that the
// gate did not grant.
type workspaceCloudReader struct {
	// live reads the workspace's current Cloud handles under its lock.
	//
	// The handles are re-read on every call rather than captured once, because
	// the Cloud handshake completes after the workspace is built: a reader that
	// snapshotted a nil gate at open time would report Standard for the rest of
	// the session no matter what the server later said.
	live func() (gate *cloud.Gate, configured bool, installation, session string, err error)

	// The captured fields are the fallback for a reader built without a live
	// source, which is how tests construct one directly.
	gate         *cloud.Gate
	configured   bool
	installation string
	session      string
	err          error
}

// read returns the current Cloud handles.
func (c *workspaceCloudReader) read() (*cloud.Gate, bool, string, string, error) {
	if c.live != nil {
		return c.live()
	}
	return c.gate, c.configured, c.installation, c.session, c.err
}

// Configured reports whether a Cloud is set up at all.
//
// A nil gate with no client means the user never configured one, which is
// different from being refused — and the difference is what the Status screen
// must show.
func (c *workspaceCloudReader) Configured() bool {
	gate, configured, _, _, _ := c.read()
	return configured || gate != nil
}

// Entitled asks the canonical gate. A nil gate answers no, which is what keeps
// the TUI's default Standard.
func (c *workspaceCloudReader) Entitled() bool {
	gate, _, _, _, _ := c.read()
	if gate == nil {
		return false
	}
	return gate.Entitled()
}

// Capabilities lists what the verified lease actually carries.
//
// The gate answers one capability at a time and deliberately exposes no list,
// so this asks it about the capabilities MARSHAL knows by name. A capability
// the client does not know about is one it could not use anyway, and asking the
// gate per name keeps the gate the only authority — this cannot report a
// capability the signed lease does not carry.
func (c *workspaceCloudReader) Capabilities() []string {
	gate, _, _, _, _ := c.read()
	if gate == nil {
		return nil
	}
	var held []string
	for _, name := range knownULTRACapabilities {
		if gate.Capability(name) {
			held = append(held, name)
		}
	}
	return held
}

// knownULTRACapabilities are the capability names this build understands.
var knownULTRACapabilities = []string{cloud.CapabilityDelegation}

func (c *workspaceCloudReader) ExpiresAt() (time.Time, bool) {
	gate, _, _, _, _ := c.read()
	if gate == nil {
		return time.Time{}, false
	}
	return gate.ExpiresAt()
}

func (c *workspaceCloudReader) RenewAt() (time.Time, bool) {
	gate, _, _, _, _ := c.read()
	if gate == nil {
		return time.Time{}, false
	}
	return gate.RenewAt()
}

func (c *workspaceCloudReader) InstallationID() string {
	_, _, installation, _, _ := c.read()
	return installation
}

func (c *workspaceCloudReader) SessionID() string {
	_, _, _, session, _ := c.read()
	return session
}

func (c *workspaceCloudReader) Err() error {
	_, _, _, _, err := c.read()
	return err
}
