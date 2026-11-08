package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/cloud"
)

func TestTelemetryFirstRunNotice(t *testing.T) {
	for _, tc := range []struct {
		name     string
		endpoint string
		disabled bool
		want     bool
	}{
		{"endpoint off", "off", false, false},
		{"user disabled", "http://127.0.0.1:1", true, false},
		{"enabled", "http://127.0.0.1:1", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", config)
			t.Setenv(cloud.EnvEndpoint, tc.endpoint)
			t.Setenv("TERM", "xterm")
			store := cloud.NewStore(filepath.Join(config, "marshal"))
			if _, _, err := store.SetTelemetryEnabled(t.Context(), !tc.disabled); err != nil {
				t.Fatal(err)
			}
			_, terminal := setupPTY(t)
			var errout bytes.Buffer
			c := command{root: t.TempDir(), stdin: terminal, stdout: terminal, stderr: &errout}
			if !c.isInteractiveTerminal() {
				t.Fatal("test requires an interactive terminal")
			}
			_, stop := c.startTelemetry(t.Context(), []string{"version"})
			stop()
			if got := strings.Contains(errout.String(), "Anonymous telemetry is on:"); got != tc.want {
				t.Fatalf("notice shown = %v, want %v: %s", got, tc.want, errout.String())
			}
			st, err := store.Load()
			if err != nil {
				t.Fatal(err)
			}
			if st.TelemetryNoticeShown != tc.want {
				t.Fatalf("notice marked shown = %v, want %v", st.TelemetryNoticeShown, tc.want)
			}
		})
	}
}
