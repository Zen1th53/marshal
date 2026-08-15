package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/policy"
)

func testPolicyRecord(t *testing.T, id string) PolicyRecord {
	t.Helper()
	p := policy.Policy{ID: policy.PolicyID(id), Version: 1, Default: policy.EffectDeny, Rules: []policy.Rule{{
		ID: "allow-read", Description: "read-only access", Effect: policy.EffectAllow,
		When: map[string]string{"action": "read"}, Priority: 10,
	}}}
	digest, err := p.Digest()
	if err != nil {
		t.Fatal(err)
	}
	return PolicyRecord{Policy: p, Binding: policy.PolicyBinding{Version: p.Version, Digest: digest, Generation: 7}, SourceRef: "repo:policy.yaml", Status: "draft"}
}

func TestPolicyStorePersistsAndReloadsCanonicalPolicy(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	record := testPolicyRecord(t, "release-gate")
	if err := st.PutPolicy(ctx, record); err != nil {
		t.Fatalf("PutPolicy: %v", err)
	}
	loaded, err := st.GetPolicy(ctx, record.Policy.ID, record.Policy.Version)
	if err != nil {
		t.Fatalf("GetPolicy: %v", err)
	}
	if loaded.Binding != record.Binding || loaded.Policy.ID != record.Policy.ID || loaded.Policy.Version != record.Policy.Version {
		t.Fatalf("loaded policy mismatch: %#v", loaded)
	}
}

func TestPolicyStoreIdempotentDuplicateAndImmutableConflict(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	record := testPolicyRecord(t, "immutable")
	if err := st.PutPolicy(ctx, record); err != nil {
		t.Fatal(err)
	}
	if err := st.PutPolicy(ctx, record); err != nil {
		t.Fatalf("idempotent retry: %v", err)
	}
	conflict := record
	conflict.Policy.Rules[0].Description = "different content"
	conflict.Binding.Digest, _ = conflict.Policy.Digest()
	if err := st.PutPolicy(ctx, conflict); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("conflict error = %v, want model conflict", err)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM policy_versions"); got != 1 {
		t.Fatalf("policy rows = %d", got)
	}
}

func TestPolicyStoreRejectsWrongDigestWithoutMutation(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	record := testPolicyRecord(t, "wrong-digest")
	record.Binding.Digest = policy.PolicyDigest("sha256:" + strings.Repeat("0", 64))
	if err := st.PutPolicy(ctx, record); err == nil {
		t.Fatal("wrong digest accepted")
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM policy_versions"); got != 0 {
		t.Fatalf("policy rows = %d", got)
	}
}

func TestPolicyStoreReloadsAfterRestartAndRejectsCorruption(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	first, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	record := testPolicyRecord(t, "restart")
	if err := first.PutPolicy(ctx, record); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	loaded, err := second.GetPolicy(ctx, record.Policy.ID, record.Policy.Version)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Binding != record.Binding {
		t.Fatalf("binding changed after restart: %#v", loaded.Binding)
	}
	if _, err := second.db.ExecContext(ctx, "UPDATE policy_versions SET normalized_rules = ? WHERE policy_id = ?", `{"id":"restart","version":1,"default":"allow","rules":[]}`, "restart"); err != nil {
		t.Fatal(err)
	}
	if _, err := second.GetPolicy(ctx, record.Policy.ID, record.Policy.Version); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("corruption error = %v", err)
	}
}

func TestPolicyStoreMultiStoreImmutableWinner(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	first, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if err := first.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	second, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	record := testPolicyRecord(t, "multi-store")
	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(2)
	go func() { defer wg.Done(); errs[0] = first.PutPolicy(ctx, record) }()
	go func() { defer wg.Done(); errs[1] = second.PutPolicy(ctx, record) }()
	wg.Wait()
	if (errs[0] != nil && errs[1] != nil) || (errs[0] != nil && !errors.Is(errs[0], model.ErrConflict)) || (errs[1] != nil && !errors.Is(errs[1], model.ErrConflict)) {
		t.Fatalf("duplicate multi-store results: %v", errs)
	}
	if got := queryInt(t, first.db, "SELECT count(*) FROM policy_versions WHERE policy_id = ?", "multi-store"); got != 1 {
		t.Fatalf("policy rows = %d", got)
	}
}

func TestPolicyStoreSecretMarkerRejectedAndAbsentFromDatabase(t *testing.T) {
	ctx := context.Background()
	marker := "MARSHAL_TEST_SECRET_T48_A02_X9"
	path := filepath.Join(t.TempDir(), "state.db")
	st, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	record := testPolicyRecord(t, "secret")
	record.SourceRef = strings.Repeat(marker, 100)
	if err := st.PutPolicy(ctx, record); err == nil || strings.Contains(err.Error(), marker) {
		t.Fatalf("secret-bearing input result = %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), marker) {
		t.Fatal("secret marker persisted in SQLite bytes")
	}
}
