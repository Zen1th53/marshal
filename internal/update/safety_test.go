package update

import (
	"strings"
	"testing"
)

func TestValidateInstall(t *testing.T) {
	for _, tt := range []struct {
		name, current, target string
		schema                int
		want                  string
	}{
		{"dev", "dev", "v1.0.0", 120, "dev build"},
		{"source", "dev+abc123", "v1.0.0", 120, "dev build"},
		{"downgrade", "v2.0.0", "v1.0.0", 120, "newer release"},
		{"same", "v1.0.0", "v1.0.0", 120, "newer release"},
		{"schema regression", "v1.0.0", "v2.0.0", 110, "schema v110"},
		{"unknown schema, newer release", "v1.0.0", "v2.0.0", 0, ""},
		{"compatible", "v1.0.0", "v2.0.0", 114, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateInstall(tt.current, Release{Tag: tt.target, SchemaVersion: tt.schema}, 114)
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
