package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/update"
)

// Extreme never contacts the public release feed and never replaces itself;
// it prints how to rebuild from the private repository and succeeds. Community
// still checks the feed: with every proxy unreachable, the check fails.
func TestUpdateEdition(t *testing.T) {
	for _, key := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"} {
		t.Setenv(key, "http://127.0.0.1:1")
	}
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	t.Setenv(update.DisableEnv, "")
	// Never reach the real feed: a newer release would be installed over
	// the test binary.
	t.Setenv(update.APIEnv, "http://127.0.0.1:1")
	for _, args := range [][]string{{"update"}, {"update", "install"}} {
		var stdout, stderr bytes.Buffer
		code := Run(context.Background(), args, nil, &stdout, &stderr)
		if update.PrivateEdition() {
			out := stdout.String()
			if code != 0 || !strings.Contains(out, "tools/build_extreme.sh") || !strings.Contains(out, "MARSHAL_EXTREME_BIN=$HOME/.local/bin/marshal tools/install_extreme.sh") || !strings.Contains(out, "never downloads or installs public releases") {
				t.Fatalf("%v: code %d stdout %q stderr %q", args, code, out, stderr.String())
			}
			continue
		}
		// Community still consults the release feed: either the unreachable
		// proxy fails the check, or (for a dev build) the feed path reports
		// the latest release. It never prints the private guidance.
		checked := code != 0 && strings.Contains(stderr.String(), "update check failed") || code == 0 && (strings.Contains(stdout.String(), "latest release") || strings.Contains(stdout.String(), "is available"))
		if !checked || strings.Contains(stdout.String(), "private build") {
			t.Fatalf("%v: community did not check the release feed: code %d stdout %q stderr %q", args, code, stdout.String(), stderr.String())
		}
	}
}
