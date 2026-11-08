//go:build !governed

package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/statepath"
	"github.com/Zen1th53/marshal/internal/store"
)

func legacyCLIProject(t *testing.T) (string, projectid.Binding) {
	t.Helper()
	root := cliRepo(t).Path()
	dir := filepath.Join(root, statepath.OtherDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	evidence := projectid.CollectEvidence(t.Context(), nil, root)
	nonce, err := projectid.NewNonce()
	if err != nil {
		t.Fatal(err)
	}
	evidence.CreatedNonce = nonce
	id, err := projectid.Derive(evidence)
	if err != nil {
		t.Fatal(err)
	}
	binding := projectid.Binding{ID: id, Evidence: evidence, RecordedRoot: root, SchemaVersion: projectid.BindingSchemaVersion}
	if err = projectid.SaveBinding(dir, binding); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(t.Context(), filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = s.InitProject(t.Context(), model.Project{ID: "PROJECT-local", Repository: root, DefaultBranch: "main", PackVersion: "6.0.0"}); err != nil {
		t.Fatal(err)
	}
	if err = s.InitMCPProposals(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = s.InitMCPMemory(t.Context()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 13; i++ {
		now := time.Now().UTC()
		rec := model.MemoryRecordV2{ID: fmt.Sprintf("MEM-cli-fixture-%02d", i), ProjectID: "PROJECT-local", Kind: model.MemoryKindSemantic, Lifecycle: model.MemoryCandidate, Authority: model.AuthorityAgent, Title: fmt.Sprintf("migration fixture %d", i), Body: fmt.Sprintf("original persisted curated observation %d", i), Scope: "project", ScopeID: "PROJECT-local", Revision: 1, ObservedAt: now, IngestedAt: now, ValidFrom: now, CreatedAt: now, UpdatedAt: now, SessionID: "fixture-original-session", Source: model.MemorySource{Kind: "live_mcp", Reference: "fixture-owner"}, ExtMeta: map[string]any{"launch_id": "fixture-launch", "provider": "codex", "canonical_project_root": root}}
		rec.ExtMeta["payload_digest"] = rec.CanonicalDigest()
		raw, _ := json.Marshal(map[string]string{"action": "memory", "id": rec.ID})
		occurrence := "mcp:" + rec.ID
		_, _, err = s.AdmitMCPMemory(t.Context(), "PROJECT-local", store.MCPProposal{ID: occurrence, Launch: "fixture-launch", Owner: "fixture-owner", Provider: "codex", Submission: rec.ID, Payload: string(raw)}, store.MCPMemoryCandidate{ImportKey: rec.ID, PayloadDigest: rec.CanonicalDigest(), Record: rec})
		if err != nil {
			t.Fatal(err)
		}
		if err = s.CommitMCPMemoryDecision(t.Context(), "PROJECT-local", occurrence, true, rec, model.Event{ID: "EVENT-" + rec.ID, ProjectID: "PROJECT-local", Type: "PERMISSION_DECIDED", Timestamp: now, Data: map[string]any{"fixture_only": true, "allow": true}}); err != nil {
			t.Fatal(err)
		}
		if err = s.SetMCPProposalState(t.Context(), "PROJECT-local", occurrence, "applied", "fixture original applied receipt"); err != nil {
			t.Fatal(err)
		}
	}
	return root, binding
}

func legacyCLIPlan(t *testing.T, root string) store.LegacyMemoryPlan {
	t.Helper()
	var out, stderr bytes.Buffer
	if code := Execute(t.Context(), root, []string{"--json", "memory", "migrate-legacy", "--source-kind", store.LegacyPrivateMemorySource, "--dry-run"}, strings.NewReader(""), &out, &stderr); code != 0 {
		t.Fatal(code, stderr.String())
	}
	var plan store.LegacyMemoryPlan
	if err := json.Unmarshal(out.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestExtremeLegacyMemoryCLIProductionInitMintsDistinctNonceAndIsNeverOverwritten(t *testing.T) {
	root, source := legacyCLIProject(t)
	plan := legacyCLIPlan(t, root)
	if err := os.MkdirAll(statepath.Path(root), 0700); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if code := Execute(t.Context(), root, []string{"init"}, strings.NewReader(""), &out, &stderr); code != 0 {
		t.Fatal(code, stderr.String())
	}
	target, found := projectid.LoadBinding(statepath.Path(root))
	if !found || target.ID == source.ID || target.Evidence.CreatedNonce == source.Evidence.CreatedNonce {
		t.Fatal("real production init did not mint an independent nonce")
	}
	for _, initialize := range []bool{false, true} {
		args := []string{"--json", "memory", "migrate-legacy", "--source-kind", store.LegacyPrivateMemorySource, "--digest", plan.Digest}
		if initialize {
			args = append(args, "--initialize-target")
		}
		out.Reset()
		stderr.Reset()
		if code := Execute(t.Context(), root, args, strings.NewReader(""), &out, &stderr); code == 0 {
			t.Fatal("existing different project identity was adopted")
		}
		after, found := projectid.LoadBinding(statepath.Path(root))
		if !found || after != target {
			t.Fatal("existing target binding was overwritten")
		}
	}
}

func TestExtremeLegacyMemoryCLIExplicitInitializationFreshRuntimeRecallAndRollback(t *testing.T) {
	root, source := legacyCLIProject(t)
	plan := legacyCLIPlan(t, root)
	if _, err := os.Stat(statepath.Path(root)); !os.IsNotExist(err) {
		t.Fatal("dry-run created target")
	}
	var out, stderr bytes.Buffer
	args := []string{"--json", "memory", "migrate-legacy", "--source-kind", store.LegacyPrivateMemorySource, "--digest", plan.Digest, "--initialize-target"}
	if code := Execute(t.Context(), root, args, strings.NewReader(""), &out, &stderr); code != 0 {
		t.Fatal(code, stderr.String())
	}
	target, found := projectid.LoadBinding(statepath.Path(root))
	if !found || target != source {
		t.Fatal("explicit private initialization lost original project identity")
	}
	runtime, err := app.Open(t.Context(), root)
	if err != nil {
		t.Fatal("fresh production runtime cannot admit migration", err)
	}
	principal := authz.Principal{ID: "cli-operator", Role: authz.Role{Name: "operator", Authorities: []authz.Authority{authz.AuthorityPolicyAdmin}}}
	for _, id := range plan.MemoryIDs {
		rec, err := runtime.Store().GetMemoryV2(t.Context(), runtime.ProjectID(), id)
		if err != nil {
			t.Fatal("fresh canonical recall", err)
		}
		result, err := runtime.Memory().Recall(t.Context(), principal, app.RecallRequest{ProjectID: runtime.ProjectID(), Query: rec.Body, MaxRecords: 1, MaxBytes: 4096, AllowedScopeIDs: []string{runtime.ProjectID()}})
		if err != nil || len(result.Results) != 1 || result.Results[0].ID != id {
			t.Fatalf("fresh derived recall failed %s err=%v results=%+v body=%q", id, err, result.Results, rec.Body)
		}
	}
	if err = runtime.Close(); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	stderr.Reset()
	if code := Execute(t.Context(), root, []string{"--json", "memory", "rollback-migration", "--digest", plan.Digest}, strings.NewReader(""), &out, &stderr); code != 0 {
		t.Fatal(code, stderr.String())
	}
	runtime, err = app.Open(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	items, err := runtime.Store().ListMemoryV2(t.Context(), store.MemoryQueryFilter{ProjectID: runtime.ProjectID()})
	if err != nil || len(items) != 0 {
		t.Fatal("fresh rollback runtime retained facts", len(items), err)
	}
}
