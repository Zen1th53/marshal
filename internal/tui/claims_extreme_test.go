//go:build !governed

package tui

import (
	"strings"
	"testing"
)

const archGoldenName, helpGoldenName = "arch", "help"

func TestShippedClaimsMatchTheBuild(t *testing.T) {
	prohibited := []string{"lease", "broker", "namespace", "proxy", "bubblewrap", "process 05"}
	stringsToCheck := map[string]string{
		"securityKeyWhat":    securityKeyWhat,
		"egressStatusHeader": egressStatusHeader,
		"sandboxStatusText":  sandboxStatusText,
		"providerEgressLine": providerEgressLine,
	}
	for name, val := range stringsToCheck {
		lower := strings.ToLower(val)
		for _, p := range prohibited {
			if strings.Contains(lower, p) {
				t.Errorf("%s contains prohibited word %q: %q", name, p, val)
			}
		}
	}
}
