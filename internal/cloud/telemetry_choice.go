package cloud

import (
	"context"
	"regexp"
	"strings"
)

// clientSemanticVersion is a version the Cloud records as it is.
var clientSemanticVersion = regexp.MustCompile(`^\d+\.\d+\.\d+(?:[-+][A-Za-z0-9.-]{1,32})?$`)

// ClientVersion is the MARSHAL version as the Cloud records it: a bare
// semantic version, without the release tag's leading "v". Development builds
// use a fixed semantic version rather than arbitrary VCS/build text.
func ClientVersion(version string) string {
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	if !clientSemanticVersion.MatchString(version) {
		return "0.0.0-dev"
	}
	return version
}

// SetTelemetry is `marshal telemetry enable|disable` for the machine state in
// dir: the choice is saved first, and a change to disabled sends the one
// bounded opt-out event when the endpoint is enabled. version is the build's
// own version; it is normalized by ClientVersion.
func SetTelemetry(ctx context.Context, dir string, enabled bool, version string) (State, bool, error) {
	st, changed, err := NewStore(dir).SetTelemetryEnabled(ctx, enabled)
	if err != nil {
		return st, changed, err
	}
	cfg := LoadConfig()
	if changed && st.TelemetryDisabled && cfg.Enabled() {
		if client, err := NewClient(cfg.Endpoint, ClientVersion(version)); err == nil {
			client.SendOptOut(ctx, st)
		}
	}
	return st, changed, nil
}
