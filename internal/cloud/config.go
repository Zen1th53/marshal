package cloud

import (
	"context"
	"os"
	"strings"

	"github.com/Zen1th53/marshal/internal/goalintake"
)

// Environment variables that configure the Community Cloud client.
const (
	// EnvEndpoint names the Cloud authority. Unset means Standard, offline.
	EnvEndpoint = "MARSHAL_CLOUD_ENDPOINT"
	// EnvExecution switches ULTRA Execution on. This is a preference, not an
	// authority: with no entitlement it changes nothing at all.
	EnvExecution = "MARSHAL_ULTRA_EXECUTION"
)

// DefaultEndpoint is the Community Cloud authority.
const DefaultEndpoint = "https://marshal.blackhat.uz"

// Config is the resolved client configuration.
type Config struct {
	// Endpoint is the Cloud authority. Empty disables the client entirely.
	Endpoint string
	// ExecutionEnabled reflects the user's preference for ULTRA Execution.
	ExecutionEnabled bool
}

// LoadConfig resolves configuration from the environment.
//
// The default is off. MARSHAL must work for someone who has never heard of the
// Community Cloud, on a machine with no network, and that is only true if
// absence of configuration means Standard rather than an error or a retry loop.
func LoadConfig() Config {
	return Config{
		Endpoint:         strings.TrimSpace(os.Getenv(EnvEndpoint)),
		ExecutionEnabled: truthy(os.Getenv(EnvExecution)),
	}
}

func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// Enabled reports whether a Cloud endpoint is configured.
func (c Config) Enabled() bool { return c.Endpoint != "" }

// Authorization is the result of asking the Cloud about this session.
//
// It carries a Gate rather than a boolean so callers cannot accidentally cache
// a stale yes: every question goes back to the gate, which re-checks expiry.
type Authorization struct {
	// Gate is the canonical decision point. It is nil when the Cloud is not
	// configured, and a nil gate answers "not entitled" to everything, so
	// callers need no special case for the offline path.
	Gate *Gate
	// Session maintains the lease. Nil when the Cloud is not configured.
	Session *Session
	// Reporter queues operational telemetry. Nil when the Cloud is not
	// configured, and a nil Reporter discards events, so callers never need to
	// check before recording something.
	Reporter *Reporter
	// Heartbeat reports presence while ULTRA is live. Nil when the Cloud is not
	// configured.
	Heartbeat *Heartbeat
	// Client, State and SessionID are what asking for an entitlement needs.
	// They are exposed because a session that is *not* entitled is exactly the
	// one that has something to ask for, and the gate deliberately does not
	// carry the installation key a request must be signed with.
	Client    *Client
	State     State
	SessionID string
	// ExecutionEnabled is the user's preference, carried through unchanged.
	ExecutionEnabled bool
	// Err records why authorization did not happen, for reporting. It is not a
	// failure of MARSHAL: an unconfigured or unreachable Cloud means Standard.
	Err error
}

// Mode reports the operating mode this authorization permits.
func (a Authorization) Mode() goalintake.Mode { return a.Gate.Mode() }

// Policy builds the delegation policy for Goal confirmation.
func (a Authorization) Policy() goalintake.DelegationPolicy {
	return a.Gate.Policy(a.ExecutionEnabled)
}

// Start runs lease maintenance, heartbeat and telemetry in the background.
//
// Each is separate on purpose: a stalled telemetry flush must not delay a lease
// renewal, and a lapsed lease must stop the heartbeat without touching the
// telemetry queue.
func (a Authorization) Start(ctx context.Context) {
	if a.Session != nil {
		go a.Session.Maintain(ctx)
	}
	if a.Heartbeat != nil {
		go a.Heartbeat.Run(ctx)
	}
	if a.Reporter != nil {
		go a.Reporter.Run(ctx)
	}
}

// Stop ends lease maintenance, heartbeat and telemetry.
//
// Order matters. The heartbeat stops first so it cannot enqueue an event after
// the final flush, and the reporter closes last so events recorded during
// shutdown are still sent.
func (a Authorization) Stop() {
	if a.Heartbeat != nil {
		a.Heartbeat.Stop()
	}
	if a.Session != nil {
		a.Session.Stop()
	}
	if a.Reporter != nil {
		a.Reporter.Close(context.Background())
	}
}

// Authorize brings up the Cloud client for one session.
//
// Every failure path returns a usable Authorization with a nil gate, so a
// caller that ignores the error still gets Standard rather than a crash or an
// accidental grant. That is deliberate: this function sits in front of ordinary
// work, and the cost of it going wrong should be a lost capability, never a
// lost session.
func Authorize(ctx context.Context, cfg Config, runtimeDir, clientVersion string) Authorization {
	result := Authorization{ExecutionEnabled: cfg.ExecutionEnabled}
	if !cfg.Enabled() {
		return result
	}

	client, err := NewClient(cfg.Endpoint, clientVersion)
	if err != nil {
		result.Err = err
		return result
	}
	result.Client = client

	store := NewStore(runtimeDir)
	state, err := store.LoadOrCreate()
	if err != nil {
		result.Err = err
		return result
	}
	result.State = state

	// Keys come from the server rather than being compiled in, so a rotation
	// does not require shipping a new MARSHAL. The trade is that key discovery
	// rides on TLS, which is why NewClient refuses plain HTTP.
	ring, err := FetchKeys(ctx, client)
	if err != nil {
		result.Err = err
		return result
	}

	sessionID, err := NewSessionID()
	if err != nil {
		result.Err = err
		return result
	}
	result.SessionID = sessionID

	gate := NewGate(state.InstallationID, sessionID, ring, nil)
	session := NewSession(client, gate, state, sessionID)
	reporter := NewReporter(client, state.InstallationID, sessionID, clientVersion, nil)

	// The reporter is attached to the result before the first call that can
	// fail, so a failure is itself reportable. Attaching it afterwards would
	// mean the events worth having — activation failures — were the ones the
	// client had no way to send.
	result.Reporter = reporter
	session.AttachReporter(reporter)
	reporter.Record(KindAppStart, TelemetryStandard, "")

	// Registration is idempotent, and a server that already knows this
	// installation answers success, so this is safe to repeat every run.
	if err := client.Register(ctx, state); err != nil {
		reporter.Record(KindErrorClass, TelemetryStandard, ClassifyError(err))
		result.Err = err
		return result
	}
	reporter.Record(KindRegistration, TelemetryStandard, "")

	if err := session.Start(ctx); err != nil {
		reporter.Record(KindActivationFail, TelemetryStandard, ClassifyError(err))
		result.Err = err
		return result
	}
	reporter.Record(KindActivationOK, TelemetryUltra, "")
	reporter.Record(KindModeChange, TelemetryUltra, "")

	result.Gate = gate
	result.Session = session
	result.Heartbeat = NewHeartbeat(client, gate, reporter, state, sessionID)
	return result
}

// FetchKeys is a package-level wrapper so tests can substitute key discovery.
func FetchKeys(ctx context.Context, c *Client) (*KeyRing, error) {
	return c.FetchKeys(ctx)
}
