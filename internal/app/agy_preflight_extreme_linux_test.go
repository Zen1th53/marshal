//go:build !governed && linux

package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/permission"
)

// A wholly owned local executable supplies metadata only; no provider binary,
// account, transport or model is used by these regressions.
func agyPreflightFixture(t *testing.T, version string) {
	t.Helper()
	script := "#!/bin/sh\ncase \"$1\" in\n--version) printf '%s\\n' '" + version + "';;\n--help) printf '%s\\n' '--print --output-format --conversation --mode --sandbox --new-project --disable-slash-commands --model';;\n*) exit 87;;\nesac\n"
	agyPreflightScriptFixture(t, script)
}

func agyPreflightScriptFixture(t *testing.T, script string) {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "agy"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1")
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
}

func TestAGYUnsupportedMetadataRefusesBeforeCredentialDecision(t *testing.T) {
	for _, provider := range []string{"agy", "antigravity"} {
		t.Run(provider, func(t *testing.T) {
			agyPreflightFixture(t, "1.3.2")
			r := openNetpolRuntime(t)
			control, err := r.OpenLocalControl(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			requests := 0
			r.SetPermissionSink(func(req permission.Request) {
				requests++
				if err := r.CommandPermission(control.Context(t.Context()), req, false, "owned regression: no credential access"); err != nil {
					t.Error(err)
				}
			})
			profile, err := r.VerifyMarshalWorker(t.Context(), provider, "gemini-3.8-flash-high")
			if requests != 0 {
				t.Fatalf("unsupported CLI requested account access before version rejection: requests=%d err=%v", requests, err)
			}
			if err == nil || !strings.Contains(err.Error(), "1.3.2") || !strings.Contains(err.Error(), "1.2.14") {
				t.Fatalf("missing finite observed/qualified mismatch: %v", err)
			}
			if profile.ProbeEvidenceID != "" || len(r.egressRuns) != 0 || len(r.honeypots) != 0 {
				t.Fatal("unsupported metadata acquired execution state")
			}
			if _, err := r.Store().GetHarnessProfile(t.Context(), "antigravity"); !errors.Is(err, model.ErrNotFound) {
				t.Fatalf("unsupported metadata retained profile: %v", err)
			}
			if r.HasCredentialGrant(t.Context(), "agy") {
				t.Fatal("unsupported metadata gained credential authority")
			}
		})
	}
}

func TestAGYMetadataFailuresNeverRequestCredentialOrLaunchModel(t *testing.T) {
	for _, tc := range []struct{ name, version, help, expected string }{
		{"empty", "printf ''", "exit 88", "did not report a version"},
		{"multiline", "printf '1.2.14\\n1.3.2\\n'", "exit 88", "observed=UNKNOWN"},
		{"private-output", "printf 'PRIVATE_CREDENTIAL_MARKER\\n'", "exit 88", "observed=UNKNOWN"},
		{"nonzero", "printf '1.2.14'; exit 7", "exit 88", "exit=7"},
		{"overflow", "head -c 70000 /dev/zero", "exit 88", "output_truncated=true"},
		{"help-missing", "printf '1.2.14'", "printf ''", "lacks required flag"},
		{"help-nonzero", "printf '1.2.14'", "exit 8", "exit=8"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agyPreflightScriptFixture(t, "#!/bin/sh\ncase \"$1\" in\n--version) "+tc.version+";;\n--help) "+tc.help+";;\n*) exit 87;;\nesac\n")
			r := openNetpolRuntime(t)
			requests := 0
			r.SetPermissionSink(func(permission.Request) { requests++ })
			_, err := r.VerifyMarshalWorker(t.Context(), "agy", "gemini-3.8-flash-high")
			if err == nil || !strings.Contains(err.Error(), tc.expected) || strings.Contains(err.Error(), "PRIVATE_CREDENTIAL_MARKER") {
				t.Fatalf("metadata failure not finite/fail-closed: %v", err)
			}
			if requests != 0 || len(r.boundCredentials.byID) != 0 || len(r.egressRuns) != 0 || len(r.honeypots) != 0 {
				t.Fatal("invalid metadata requested accounts, transport or model state")
			}
		})
	}
}

func TestAGYMetadataUsesHostRunnerWithoutBwrap(t *testing.T) {
	host := t.TempDir()
	write := filepath.Join(host, "host-runner")
	script := fmt.Sprintf("#!/bin/sh\nprintf 'host' > %q\nprintf '1.3.2\\n'\n", write)
	agyPreflightScriptFixture(t, script)
	if err := privateHarnessProbePreflight(t.Context(), "agy"); err == nil || !strings.Contains(err.Error(), "observed=1.3.2") {
		t.Fatalf("host metadata version check not enforced: %v", err)
	}
	if data, err := os.ReadFile(write); err != nil || string(data) != "host" {
		t.Fatalf("metadata did not execute on host: %q %v", data, err)
	}
}

func TestAGYMetadataSocketAttemptIsTerminalWithoutPermission(t *testing.T) {
	agyPreflightScriptFixture(t, "#!/usr/bin/python3 -I -B\nimport socket\nsocket.socket().connect(('127.0.0.1', 443))\nprint('1.2.14')\n")
	r := openNetpolRuntime(t)
	requests := 0
	r.SetPermissionSink(func(permission.Request) { requests++ })
	if _, err := r.VerifyMarshalWorker(t.Context(), "agy", "gemini-3.8-flash-high"); err == nil || requests != 0 {
		t.Fatalf("metadata socket became permission/execution authority: requests=%d err=%v", requests, err)
	}
}

func TestAGYMetadataParentCancellationAndDeadlineStayFinite(t *testing.T) {
	agyPreflightScriptFixture(t, "#!/bin/sh\nsleep 30\nprintf '1.2.14'\n")
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelled), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 75*time.Millisecond)
			defer cancel()
			if cancelled {
				cancel()
			}
			start := time.Now()
			err := privateHarnessProbePreflight(ctx, "agy")
			if err == nil || time.Since(start) > time.Second {
				t.Fatalf("parent limit lost: %v", err)
			}
		})
	}
}

func TestAGYMetadataPreflightIgnoresOtherProviders(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, provider := range []string{"codex", "claude", "opencode", "gemini"} {
		if err := privateHarnessProbePreflight(ctx, provider); err != nil {
			t.Fatalf("other provider changed: %s %v", provider, err)
		}
	}
}

func TestAGYSupportedMetadataDoesNotGrantCredentialOrQualification(t *testing.T) {
	agyPreflightFixture(t, "1.2.14")
	r := openNetpolRuntime(t)
	requests := 0
	r.SetPermissionSink(func(permission.Request) { requests++ })
	// Metadata itself grants nothing. Once it passes, the real host probe gets
	// exactly one fresh probe-bound lease, which it consumes and closes; a
	// fixture that never replies correctly is not qualified.
	profile, err := r.VerifyMarshalWorker(t.Context(), "agy", "gemini-3.8-flash-high")
	if requests != 0 || err == nil {
		t.Fatalf("probe credential queued or fixture qualified: requests=%d err=%v", requests, err)
	}
	events, listErr := r.Store().ListEvents(t.Context())
	if listErr != nil {
		t.Fatal(listErr)
	}
	fresh := 0
	for _, e := range events {
		if e.Type == "PERMISSION_DECIDED" && e.Data["kind"] == "credential-bound" && e.Data["object"] == "agy" && e.Data["allow"] == true && e.Data["decided_by"] == credentialRuntimeDecider {
			scope, _ := e.Data["bound_scope"].(string)
			if !strings.Contains(scope, `"Purpose":"probe"`) {
				t.Fatalf("metadata granted a non-probe Marshal credential: %+v", e.Data)
			}
			fresh++
		}
	}
	if fresh != 1 {
		t.Fatalf("probe credential not one fresh bounded grant: %d (probe error: %v)", fresh, err)
	}
	r.boundCredentials.mu.Lock()
	for _, lease := range r.boundCredentials.byID {
		if !lease.closed {
			r.boundCredentials.mu.Unlock()
			t.Fatalf("probe lease %s stayed open after verification", lease.id)
		}
	}
	r.boundCredentials.mu.Unlock()
	if r.HasCredentialGrant(t.Context(), "agy") {
		t.Fatal("metadata retained a Marshal credential")
	}
	if profile.ProbeEvidenceID != "" || len(r.egressRuns) != 0 {
		t.Fatal("metadata became qualification or transport authority")
	}
}

func TestAGYHostHarnessVerificationSucceedsWithoutBwrap(t *testing.T) {
	agyPreflightScriptFixture(t, `#!/bin/sh
case "$1" in
--version) printf '1.2.14\n'; exit 0;;
--help) printf '%s\n' '--print --output-format --conversation --mode --sandbox --new-project --disable-slash-commands --model'; exit 0;;
esac
printf '%s\n' '{"status":"SUCCESS","conversation_id":"host-probe","response":"MARSHAL_PROBE_OK"}'
`)
	r := openNetpolRuntime(t)
	profile, err := r.VerifyMarshalWorker(t.Context(), "agy", "gemini-3.8-flash-high")
	if err != nil {
		t.Fatal(err)
	}
	if profile.ProbeEvidenceID == "" || profile.FeatureSupport["sandbox"] != model.StatusUnsupported || profile.FeatureSupport["network"] != model.StatusUnsupported {
		t.Fatalf("host probe recorded wrong capabilities: %+v", profile)
	}
	events, err := r.Store().ListEvents(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.ID == profile.ProbeEvidenceID && event.Type == "HARNESS_VERIFIED" {
			return
		}
	}
	t.Fatal("host probe did not retain verification evidence")
}
