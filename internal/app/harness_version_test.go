package app

import "testing"

func TestSameProbedCLIVersionNormalizesOpenCodeOnly(t *testing.T) {
	for _, c := range []struct {
		provider, installed, probed string
		want                        bool
	}{
		{"opencode", "opencode v2.1.4", "2.1.4", true},
		{"opencode", "opencode v2.1.5", "2.1.4", false},
		{"opencode", "", "", true},
		{"opencode", "garbage", "", false},
		{"claude", "2.1.312 (Claude Code)", "2.1.312 (Claude Code)", true},
		{"claude", "claude v2.1.312", "2.1.312", false},
	} {
		if got := sameProbedCLIVersion(c.provider, c.installed, c.probed); got != c.want {
			t.Fatalf("%+v: got %v", c, got)
		}
	}
}
