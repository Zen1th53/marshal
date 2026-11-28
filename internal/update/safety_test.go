package update

import (
	"strings"
	"testing"
)

func TestValidateInstall(t *testing.T) {
	for _, tt := range []struct {
		name, current, target string
		schema                int
		allow                 bool
		want                  string
	}{
		{"dev", "dev", "v1.0.0", 120, true, "dev build"},
		{"source", "dev+abc123", "v1.0.0", 120, true, "dev build"},
		{"downgrade", "v2.0.0", "v1.0.0", 120, true, "newer release"},
		{"same", "v1.0.0", "v1.0.0", 120, true, "newer release"},
		{"schema regression", "v1.0.0", "v2.0.0", 110, true, "schema v110"},
		{"unknown", "v1.0.0", "v2.0.0", 0, false, "--allow-downgrade"},
		{"unknown accepted", "v1.0.0", "v2.0.0", 0, true, ""},
		{"compatible", "v1.0.0", "v2.0.0", 114, false, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateInstall(tt.current, Release{Tag: tt.target, SchemaVersion: tt.schema}, 114, tt.allow)
			if tt.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want %q", err, tt.want)
			}
		})
	}
}
