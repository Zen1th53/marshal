package cloud

import (
	"context"
	"os"
	"strings"

	"github.com/Zen1th53/marshal/internal/goalintake"
)

// EnvEndpoint overrides the default Community Cloud authority.
//
// There is no variable that switches ULTRA Execution on: a session starts in
// Standard and the person turns execution on inside the TUI with /ultra start.
const EnvEndpoint = "MARSHAL_CLOUD_ENDPOINT"

// DefaultEndpoint is the Community Cloud authority.
const DefaultEndpoint = "https://marshal.blackhat.uz"

// Config is the resolved client configuration.
type Config struct {
	// Endpoint is the Cloud authority. Empty disables the client entirely.
	Endpoint string
	// TelemetryManaged means the process-wide reporter owns telemetry.
	TelemetryManaged bool
}

// LoadConfig resolves configuration from the environment.
//
// Standard remains usable offline. Telemetry runs separately in the background;
// failure to contact the authority never grants ULTRA.
func LoadConfig() Config {
	return Config{Endpoint: resolveEndpoint(os.Getenv(EnvEndpoint))}
}

// resolveEndpoint decides which Cloud authority to ask, if any.
//
// An unset variable means "the Community Cloud", not "no cloud": without the
// default, a fresh installation has nobody to send /ultra request to, so it can
// never obtain the entitlement it is being told to ask for. The environment
// still overrides, and an explicit off value still disables the client
// outright, so running fully offline remains possible and deliberate.
//
// Reaching the default authority grants nothing on its own. It asks; the answer
// is a signed lease or a refusal, and every capability check still goes through
// the gate.
func resolveEndpoint(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return DefaultEndpoint
	}
	switch strings.ToLower(value) {
	case "off", "none", "disabled", "0", "false":
		return ""
	}
	return value
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
	// Err records why authorization did not happen, for reporting. It is not a
	// failure of MARSHAL: an unconfigured or unreachable Cloud means Standard.
	Err error
}

// Mode reports the operating mode this authorization permits.
func (a Authorization) Mode() goalintake.Mode { return a.Gate.Mode() }

// Policy builds the delegation policy for Goal confirmation. Outside the TUI
// there is nowhere to turn ULTRA Execution on, so the person is always asked.
func (a Authorization) Policy() goalintake.DelegationPolicy {
	return a.Gate.Policy(false)
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
