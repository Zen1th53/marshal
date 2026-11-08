//go:build !governed && linux

package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFreshAGYVerificationPinsProbeAndPersistsExactModel(t *testing.T) {
	for _, selected := range []string{"", "gemini-4-argon"} {
		t.Run("model="+selected, func(t *testing.T) {
			capture := filepath.Join(t.TempDir(), "model")
			t.Setenv("MARSHAL_TEST_MODEL", capture)
			agyPreflightScriptFixture(t, `#!/bin/sh
case "$1" in
--version) echo 1.2.14; exit 0;;
--help) echo '--print --output-format --conversation --mode --sandbox --new-project --disable-slash-commands --model'; exit 0;;
esac
while [ "$#" -gt 0 ]; do
 if [ "$1" = --model ]; then shift; printf '%s' "$1" > "$MARSHAL_TEST_MODEL"; break; fi
 shift
done
printf '%s\n' '{"status":"SUCCESS","conversation_id":"fresh-probe","response":"MARSHAL_PROBE_OK"}'
`)
			r := openNetpolRuntime(t)
			profile, err := r.VerifyMarshalWorker(t.Context(), "agy", selected)
			if err != nil {
				t.Fatal(err)
			}
			want := selected
			if want == "" {
				want = "gemini-3.8-flash-high"
			}
			raw, err := os.ReadFile(capture)
			if err != nil || string(raw) != want {
				t.Fatalf("probe delegated selection to CLI: model=%q err=%v", raw, err)
			}
			stored, err := r.Store().GetHarnessProfile(t.Context(), "antigravity")
			if err != nil || stored.DefaultModel != want || stored.ProbeEvidenceID == "" || stored.ProbeEvidenceID != profile.ProbeEvidenceID {
				t.Fatalf("exact probe not retained: %+v %v", stored, err)
			}
		})
	}
}
