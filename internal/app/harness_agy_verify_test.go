package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/adapter/antigravity"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestAgyHarnessVerificationRecordsNativeCapabilities(t *testing.T) {
	repo := runtimeRepo(t)
	if _, err := Bootstrap(t.Context(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	rt, err := Open(t.Context(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	dir := t.TempDir()
	path := filepath.Join(dir, "agy")
	stub := `#!/bin/sh
case "$1" in
--version) printf 'native-probe-version\n'; exit 0;;
--help) printf '%s\n' '--print --output-format --model --mode --conversation --sandbox --new-project --disable-slash-commands'; exit 0;;
esac
printf '%s\n' '{"status":"SUCCESS","conversation_id":"real-probe-contract","response":"MARSHAL_PROBE_OK"}'
`
	if err := os.WriteFile(path, []byte(stub), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	profile, err := rt.VerifyNativeAgyWorker(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	if profile.Harness != "antigravity" || profile.ProbeEvidenceID == "" || profile.FeatureSupport["headless"] != model.StatusNative || profile.FeatureSupport["sandbox"] != model.StatusUnsupported || profile.FeatureSupport["network"] != model.StatusUnsupported {
		t.Fatalf("untruthful native capability profile: %+v", profile)
	}
	events, err := rt.Store().ListEvents(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range events {
		if e.ID == profile.ProbeEvidenceID && e.Type == "NATIVE_HARNESS_VERIFIED" && e.Data["provider"] == "agy" {
			found = true
		}
	}
	if !found {
		t.Fatal("native probe has no separate durable evidence")
	}
	s := rt.Marshal()
	s.InstalledVersion = installedCLIVersion
	if !s.workerGovernance(t.Context(), "agy").Governed() {
		t.Fatal("observed native capability contract not admitted")
	}
	for name, script := range map[string]string{
		"wrong reply":       strings.Replace(stub, `"response":"MARSHAL_PROBE_OK"`, `"response":"not the reply"`, 1),
		"no native session": strings.Replace(stub, `"conversation_id":"real-probe-contract",`, "", 1),
		"worktree mutation": strings.Replace(stub, "printf '%s\\n' '{", "printf 'unexpected\\n' > unexpected.txt\nprintf '%s\\n' '{", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			if _, err := rt.VerifyNativeAgyWorker(t.Context(), ""); err == nil {
				t.Fatal("invalid probe minted evidence")
			}
			stored, err := rt.Store().GetHarnessProfile(t.Context(), "antigravity")
			if err != nil || stored.ProbeEvidenceID != profile.ProbeEvidenceID {
				t.Fatal("invalid probe replaced accepted evidence")
			}
		})
	}
	rt.adapters = map[string]adapter.Adapter{"agy": antigravity.New("", nil)}
	if _, err := rt.VerifyNativeAgyWorker(t.Context(), ""); err == nil {
		t.Fatal("injected adapter established native capabilities")
	}
	rt.adapters = nil
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.VerifyNativeAgyWorker(t.Context(), ""); err == nil {
		t.Fatal("failed native provider minted probe evidence")
	}
	stored, err := rt.Store().GetHarnessProfile(t.Context(), "antigravity")
	if err != nil || stored.ProbeEvidenceID != profile.ProbeEvidenceID {
		t.Fatal("failed probe replaced successful evidence")
	}
}

func TestNativeCapabilityProfileCannotAuthorizeGovernedTask(t *testing.T) {
	s, _ := marshalFixture(t, 1)
	wire(t, s)
	p, err := s.Store.GetHarnessProfile(t.Context(), "worker")
	if err != nil {
		t.Fatal(err)
	}
	p.FeatureSupport = map[string]model.FeatureStatus{"headless": model.StatusNative, "sandbox": model.StatusUnsupported, "network": model.StatusUnsupported}
	if err := s.Store.SaveHarnessProfile(t.Context(), *p); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartPlanning(t.Context(), "native-profile", "write", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(t.Context(), "native-profile"); err != nil {
		t.Fatal(err)
	}
	run, err := s.Execute(t.Context(), "native-profile", marshalBrief, nil)
	if err != nil {
		t.Fatal(err)
	}
	if run.State != marshal.AwaitingUser || run.Tasks[0].State != marshal.Escalated {
		t.Fatalf("native-only evidence launched governed task: %s/%s", run.State, run.Tasks[0].State)
	}
}
