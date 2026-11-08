package cloud

import (
	"context"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

const telemetryTimeout = 750 * time.Millisecond
const startupTelemetryTimeout = 300 * time.Millisecond
const telemetryInterval = 2 * time.Minute

// ActiveMode shares the canonical lease gate and execution bit with the reporter.
// No workspace, prompt or project data crosses this boundary.
type ActiveMode struct {
	mu        sync.RWMutex
	gate      *Gate
	execution func() bool
}

func (m *ActiveMode) Attach(g *Gate) { m.mu.Lock(); m.gate = g; m.mu.Unlock() }

// ObserveExecution distinguishes a held entitlement from active ULTRA work.
func (m *ActiveMode) ObserveExecution(enabled func() bool) {
	m.mu.Lock()
	m.execution = enabled
	m.mu.Unlock()
}

func (m *ActiveMode) Mode() TelemetryMode { return m.modeForInstallation("") }

func (m *ActiveMode) modeForInstallation(installation string) TelemetryMode {
	m.mu.RLock()
	gate, enabled := m.gate, m.execution
	m.mu.RUnlock()
	if gate.Entitled() && (installation == "" || gate.localEntitlement() || gate.installationID == installation) && (enabled == nil || enabled()) {
		return TelemetryUltra
	}
	return TelemetryStandard
}

// withState serializes identity/preference updates across processes using the
// existing private machine-state lock implementation. Never lock across HTTP.
func (s *Store) withState(ctx context.Context, fn func() error) error {
	key := RequestKey{Endpoint: DefaultEndpoint, InstallationID: "inst-0000000000000000", SessionID: "sess-0000000000000000"}
	return NewRequestStore(filepath.Dir(s.path)).locked(ctx, key, func(string) error { return fn() })
}

// TelemetryPreference reads the machine choice, creating the identity if needed.
func (s *Store) TelemetryPreference(ctx context.Context) (State, error) {
	var st State
	err := s.withState(ctx, func() error {
		var err error
		st, err = s.LoadOrCreate()
		return err
	})
	return st, err
}

// TakeTelemetryNotice persists the one-time interactive notice before returning.
func (s *Store) TakeTelemetryNotice(ctx context.Context) bool {
	show := false
	_ = s.withState(ctx, func() error {
		st, err := s.LoadOrCreate()
		if err != nil || st.TelemetryDisabled || st.TelemetryNoticeShown {
			return err
		}
		st.TelemetryNoticeShown = true
		if err := s.Save(st); err != nil {
			return err
		}
		show = true
		return nil
	})
	return show
}

// SetTelemetryEnabled persists the choice before attempting any network call.
// An explicit opt-out is permanent at the server: enable mints a new identity
// and keypair, also invalidating the old identity's cached ULTRA authorization.
func (s *Store) SetTelemetryEnabled(ctx context.Context, enabled bool) (State, bool, error) {
	var st State
	changed := false
	err := s.withState(ctx, func() error {
		var err error
		st, err = s.LoadOrCreate()
		if err != nil || st.TelemetryDisabled == !enabled {
			return err
		}
		if enabled {
			shown := st.TelemetryNoticeShown
			st, err = NewInstallation()
			if err != nil {
				return err
			}
			st.TelemetryNoticeShown = shown
		} else {
			st.TelemetryDisabled = true
		}
		if err := s.Save(st); err != nil {
			return err
		}
		changed = true
		return nil
	})
	return st, changed, err
}

// SendOptOut makes exactly one bounded attempt, with precisely the five fields
// required by CLIENT-CONTRACT.md. It bypasses the normal disabled-event guard.
func (c *Client) SendOptOut(ctx context.Context, st State) {
	ctx, cancel := context.WithTimeout(ctx, telemetryTimeout)
	defer cancel()
	_ = c.post(ctx, "/v1/telemetry/events", []struct {
		Kind           string `json:"kind"`
		InstallationID string `json:"installation_id"`
		ClientVersion  string `json:"client_version"`
		OS             string `json:"os"`
		Arch           string `json:"arch"`
	}{{"telemetry_opt_out", st.InstallationID, c.version, runtime.GOOS, runtime.GOARCH}}, nil)
}

// StartInstallationTelemetry starts a single bounded background worker. Startup
// and shutdown never wait for the network, including for one-shot commands.
// Failures are silent drops; nothing is spooled to disk.
func StartInstallationTelemetry(ctx context.Context, cfg Config, dir, version string, mode *ActiveMode) context.CancelFunc {
	ctx, cancel := context.WithCancel(ctx)
	if !cfg.Enabled() {
		return cancel
	}
	store := NewStore(dir)
	started := sendInstallationStart(ctx, cfg, store, version, mode)
	go runInstallationTelemetry(ctx, cfg, store, version, mode, telemetryInterval, started)
	return cancel
}

// sendInstallationStart makes one bounded foreground attempt so short-lived
// commands have a chance to report their start. Every failure is deliberately
// silent; telemetry never changes command success.
func sendInstallationStart(ctx context.Context, cfg Config, store *Store, version string, mode *ActiveMode) bool {
	if !cfg.Enabled() {
		return false
	}
	client, err := NewClient(cfg.Endpoint, version)
	if err != nil {
		return false
	}
	client.store = store
	client.telemetryOnly = true
	attempt, cancel := context.WithTimeout(ctx, startupTelemetryTimeout)
	defer cancel()
	return sendInstallationStartClient(attempt, client, store, version, mode)
}

func sendInstallationStartClient(ctx context.Context, client *Client, store *Store, version string, mode *ActiveMode) bool {
	client.store = store
	client.telemetryOnly = true
	st, err := store.TelemetryPreference(ctx)
	if err != nil || st.TelemetryDisabled {
		return false
	}
	if _, err := client.ensureRegistered(ctx, st, false); err != nil {
		return false
	}
	reporter := NewReporter(client, st.InstallationID, "", version, nil)
	reporter.Record(KindAppStart, mode.modeForInstallation(st.InstallationID), "")
	reporter.Record(KindHeartbeat, mode.modeForInstallation(st.InstallationID), "")
	return reporter.Flush(ctx) == nil
}

func runInstallationTelemetry(ctx context.Context, cfg Config, store *Store, version string, mode *ActiveMode, interval time.Duration, started bool) {
	client, err := NewClient(cfg.Endpoint, version)
	if err != nil {
		return
	}
	runInstallationTelemetryClientStarted(ctx, client, store, version, mode, interval, started)
}

func runInstallationTelemetryClient(ctx context.Context, client *Client, store *Store, version string, mode *ActiveMode, interval time.Duration) {
	runInstallationTelemetryClientStarted(ctx, client, store, version, mode, interval, false)
}

func runInstallationTelemetryClientStarted(ctx context.Context, client *Client, store *Store, version string, mode *ActiveMode, interval time.Duration, started bool) {
	client.store = store
	client.telemetryOnly = true
	st, err := store.TelemetryPreference(ctx)
	if err != nil {
		return
	}
	reporter := NewReporter(client, st.InstallationID, "", version, nil)
	tick := time.NewTicker(interval)
	defer tick.Stop()
	if started {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
	for {
		current, err := store.Load()
		if err != nil {
			return
		}
		if current.InstallationID != st.InstallationID {
			st = current
			reporter = NewReporter(client, st.InstallationID, "", version, nil)
			started = false
		}
		if !current.TelemetryDisabled {
			attempt, cancel := context.WithTimeout(ctx, telemetryTimeout)
			_, err = client.ensureRegistered(attempt, st, false)
			if err == nil {
				if !started {
					reporter.Record(KindAppStart, mode.modeForInstallation(st.InstallationID), "")
					started = true
				}
				reporter.Record(KindHeartbeat, mode.modeForInstallation(st.InstallationID), "")
				_ = reporter.Flush(attempt)
			}
			cancel()
			// No retries/backlog: a later heartbeat describes current activity.
			reporter.mu.Lock()
			reporter.queue = nil
			reporter.mu.Unlock()
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// PrepareEntitlementRequest enrolls the machine identity without trying to
// acquire a lease. Request observations never constitute authorization.
func PrepareEntitlementRequest(ctx context.Context, cfg Config, dir, version string) (*Client, State, string, error) {
	client, err := NewClient(cfg.Endpoint, version)
	if err != nil {
		return nil, State{}, "", err
	}
	client.store = NewStore(dir)
	st, err := client.store.TelemetryPreference(ctx)
	if err != nil {
		return nil, State{}, "", err
	}
	session, err := NewSessionID()
	if err != nil {
		return nil, State{}, "", err
	}
	if _, err := client.ensureRegistered(ctx, st, false); err != nil {
		return nil, State{}, "", err
	}
	return client, st, session, nil
}
