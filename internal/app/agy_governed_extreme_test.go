//go:build !governed && linux

package app

import (
	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/permission"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAGYTypedOwnerCredentialGrantAndRevoke(t *testing.T) {
	r := runtimeForPlan(t)
	local, err := r.OpenLocalControl(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	req := permission.Request{Kind: "credential", Object: "agy"}
	if err := r.CommandPermission(local.Context(t.Context()), req, true, "synthetic regression: exact delegated owner grant only"); err != nil {
		t.Fatal(err)
	}
	if !r.HasCredentialGrant(t.Context(), "agy") || r.HasCredentialGrant(t.Context(), "gemini") {
		t.Fatal("native AGY grant borrowed another provider scope")
	}
	if err := r.CommandPermission(local.Context(t.Context()), req, false, "synthetic regression: immediate revoke"); err != nil {
		t.Fatal(err)
	}
	if r.HasCredentialGrant(t.Context(), "agy") {
		t.Fatal("native AGY revoke was not durable")
	}
}

func TestAGYGovernedExactEndpointAndCredentialIdentity(t *testing.T) {
	for _, provider := range []string{"agy", "antigravity"} {
		endpoint, err := providerEndpoint(provider, "gemini-3.8-flash-high")
		if err != nil || endpoint != "daily-cloudcode-pa.googleapis.com:443" {
			t.Fatalf("native endpoint %s: %q %v", provider, endpoint, err)
		}
	}
	if !credentialProvider("agy") {
		t.Fatal("native AGY has no separate project credential consent")
	}
	if credentialProvider("google-account") {
		t.Fatal("unrecognized credential scope accepted")
	}
}
func TestAGYHarnessCannotImportInjectedProbe(t *testing.T) {
	r := &Runtime{adapters: map[string]adapter.Adapter{"agy": &netpolMockAdapter{}}}
	for _, provider := range []string{"agy", "antigravity"} {
		if _, err := r.VerifyMarshalWorker(t.Context(), provider, "gemini-3.8-flash-high"); err == nil || !strings.Contains(err.Error(), "injected adapters") {
			t.Fatalf("%s: %v", provider, err)
		}
	}
}
func TestAGYModelPlanRequiresCurrentExactProfile(t *testing.T) {
	r := runtimeForPlan(t)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "agy"), []byte("#!/bin/sh\nprintf '1.2.14\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	req := planCreateRequest()
	req.Checks = map[string][]string{"fix": {"true"}}
	req.TaskProviders = map[string]string{"fix": "antigravity"}
	if _, err := r.Plans().Create(t.Context(), req); err == nil {
		t.Fatal("AGY with no verified profile admitted")
	}
	profile := model.HarnessProfile{Harness: "antigravity", InstalledVersion: "1.2.14", SupportedModels: []string{"gemini-3.8-flash-high"}, DefaultModel: "gemini-3.8-flash-high", ProbeEvidenceID: "FIXTURE-AGY-no-native-call", ProbedAt: time.Now().UTC()}
	if err := r.Store().SaveHarnessProfile(t.Context(), profile); err != nil {
		t.Fatal(err)
	}
	created, err := r.Plans().Create(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := marshalPlanTaskWorker(created, created.Tasks[0])
	if err != nil || worker != "agy" || created.Routes["fix"].Provider != "agy" || created.Routes["fix"].Model != "gemini-3.8-flash-high" {
		t.Fatalf("canonical AGY assignment %q %+v %v", worker, created.Routes["fix"], err)
	}
	profile.InstalledVersion = "stale"
	if err := r.Store().SaveHarnessProfile(t.Context(), profile); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Plans().Create(t.Context(), req); err == nil {
		t.Fatal("stale native AGY profile admitted")
	}
}

func TestAGYRuntimeReportsMissingCLIWithoutProviderFallback(t *testing.T) {
	t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1")
	r := runtimeForPlan(t)
	t.Setenv("PATH", t.TempDir())
	_, _, err := r.resolveAdapter(t.Context(), "agy", model.Task{ID: "TASK-AGY-absent", Title: "synthetic setup", Risk: model.R1}, t.TempDir(), "runtime", false, "gemini-3.8-flash-high", "")
	if err == nil || !strings.Contains(err.Error(), "agy CLI is missing") {
		t.Fatalf("native AGY builder did not resolve its exact executable: %v", err)
	}
}
func TestAGYInvalidProfileModelRefusedBeforeEgress(t *testing.T) {
	r := &Runtime{}
	for _, selector := range []string{"--no-sandbox", "gemini valid", "gemini\nvalid", "invalid/provider", strings.Repeat("x", 129)} {
		if _, err := r.harnessProbeModel(t.Context(), "agy", selector); err == nil {
			t.Fatal("unsafe AGY selector reached provider setup")
		}
	}
	if got, err := r.harnessProbeModel(t.Context(), "agy", "gemini-3.8-flash-high"); err != nil || got != "gemini-3.8-flash-high" {
		t.Fatalf("exact native selector lost: %q %v", got, err)
	}
}
