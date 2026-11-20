package cloud

import (
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// releaseBuild is "true" only in a published release binary.
// tools/build_release.sh, which the release workflow runs, sets it with
//
//	-ldflags "-X github.com/Zen1th53/marshal/internal/cloud.releaseBuild=true"
//
// Nothing else does. A plain `go build`, `go run`, `go test`,
// tools/build_extreme.sh or an e2e build leaves it empty: such a binary is a
// development build and is never counted as an installation of Marshal.
var releaseBuild string

// allowProductionInTests lets this package's own tests resolve the production
// endpoint. They must still replace the HTTP transport: a client built for the
// production host inside a test binary refuses every request (see NewClient).
var allowProductionInTests bool

// ErrDevelopmentBuild is returned when a request to the production Community
// Cloud is attempted from a test binary.
var ErrDevelopmentBuild = errors.New("cloud: development builds and tests never contact the production Community Cloud")

// ReleaseBuild reports whether this binary is a published release. A test
// binary never is.
func ReleaseBuild() bool {
	return releaseBuild == "true" && !testing.Testing()
}

// productionAllowed reports whether this process may talk to the production
// Community Cloud: registration, heartbeats and ULTRA requests all count it as
// a real installation, so only release binaries do.
func productionAllowed() bool {
	return productionAllowedFor(testing.Testing(), allowProductionInTests, releaseBuild)
}

func productionAllowedFor(inTest, testOverride bool, release string) bool {
	if inTest {
		return testOverride
	}
	return release == "true"
}

// IsProductionEndpoint reports whether endpoint names the production
// Community Cloud host, however it is spelled.
func IsProductionEndpoint(endpoint string) bool {
	u, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil {
		return false
	}
	prod, _ := url.Parse(DefaultEndpoint)
	return strings.EqualFold(strings.TrimSuffix(u.Hostname(), "."), prod.Hostname())
}

// userConfigDir is os.UserConfigDir, except that a test binary honours
// XDG_CONFIG_HOME on every platform. On macOS os.UserConfigDir ignores it and
// returns ~/Library/Application Support, so tests that isolated themselves
// with XDG_CONFIG_HOME were reading and rewriting the developer's real
// machine identity (and could leave it opted out of telemetry for good).
func userConfigDir() (string, error) {
	if testing.Testing() {
		if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" && filepath.IsAbs(dir) {
			return dir, nil
		}
	}
	return os.UserConfigDir()
}

// refuseTransport fails every request without opening a connection.
type refuseTransport struct{}

func (refuseTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, ErrDevelopmentBuild
}
