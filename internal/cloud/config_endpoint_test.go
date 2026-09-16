package cloud

import "testing"

// A fresh installation must have somebody to send /ultra request to. Without a
// default authority the entitlement flow is a closed loop: the client tells the
// user to request ULTRA, and has nowhere to send the request.
func TestLoadConfigDefaultsToTheCommunityCloud(t *testing.T) {
	t.Setenv(EnvEndpoint, "")
	cfg := LoadConfig()
	if cfg.Endpoint != DefaultEndpoint {
		t.Errorf("unset endpoint = %q, want the default authority %q", cfg.Endpoint, DefaultEndpoint)
	}
	if !cfg.Enabled() {
		t.Error("a defaulted endpoint left the client disabled")
	}
}

func TestEndpointResolution(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		want  string
	}{
		{"unset takes the default", "", DefaultEndpoint},
		{"whitespace takes the default", "   ", DefaultEndpoint},
		{"explicit endpoint wins", "https://cloud.example.test", "https://cloud.example.test"},
		{"surrounding whitespace is trimmed", "  https://cloud.example.test  ", "https://cloud.example.test"},
		// Running fully offline must stay possible and deliberate.
		{"off disables", "off", ""},
		{"none disables", "none", ""},
		{"disabled disables", "DISABLED", ""},
		{"zero disables", "0", ""},
		{"false disables", "false", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveEndpoint(tc.value); got != tc.want {
				t.Errorf("resolveEndpoint(%q) = %q, want %q", tc.value, got, tc.want)
			}
		})
	}
}

// Defaulting the endpoint must not hand anybody ULTRA. Reaching an authority is
// asking, not being granted.
func TestDefaultEndpointGrantsNothing(t *testing.T) {
	t.Setenv(EnvEndpoint, "")
	t.Setenv(EnvExecution, "1")
	cfg := LoadConfig()
	if !cfg.Enabled() || !cfg.ExecutionEnabled {
		t.Fatal("test setup did not enable the local preference")
	}
	// A nil gate is what an unauthorized session holds, and it must refuse.
	var gate *Gate
	assertNoULTRA(t, gate, "defaulted endpoint with execution preference on")
}
