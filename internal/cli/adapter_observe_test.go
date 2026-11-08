package cli

import (
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/opencodecli"
)

func TestAdaptersReportOpenCodeVersionHonestly(t *testing.T) {
	twoX := opencodecli.Contract{RawVersion: "opencode v2.1.4", Version: "2.1.4", Run: true, FormatJSON: true, Model: true, Standalone: true}
	obs := judgeOpenCode(twoX, "1.18.16")
	if !obs.Available || obs.Version != "2.1.4" || obs.Pinned != "1.18.16" || !strings.Contains(obs.Detail, "differs from pinned 1.18.16; run contract verified") {
		t.Fatalf("verified 2.x not reported with both versions: %+v", obs)
	}
	broken := twoX
	broken.Standalone = false
	obs = judgeOpenCode(broken, "1.18.16")
	if obs.Available || !strings.Contains(obs.Detail, "--standalone") || !strings.Contains(obs.Detail, "pinned 1.18.16") {
		t.Fatalf("contract mismatch reported AVAILABLE: %+v", obs)
	}
	if obs := judgeOpenCode(twoX, ""); !obs.Available || strings.Contains(obs.Detail, "pinned") {
		t.Fatalf("unpinned edition: %+v", obs)
	}
	if obs := judgeOpenCode(opencodecli.Contract{RawVersion: "garbage", Run: true, FormatJSON: true, Model: true}, ""); obs.Available {
		t.Fatalf("unparsable version accepted: %+v", obs)
	}
}
