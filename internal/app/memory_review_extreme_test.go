//go:build !governed

package app

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Zen1th53/marshal/internal/model"
	"strings"
	"testing"
	"time"
)

func TestOperatorMemoryReadCannotBeForgedTransferredOrExpired(t *testing.T) {
	r, _, source, _ := requiredContextFixture(t)
	control, err := r.OpenLocalControl(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx := control.Context(t.Context())
	rec := source
	rec.ID = "MEM-operator-read-candidate"
	rec.Lifecycle = model.MemoryCandidate
	rec.Authority = model.AuthorityAgent
	rec.Source.Kind = "runtime_service"
	rec.Revision = 1
	if err = r.store.WriteMemoryV2(ctx, rec); err != nil {
		t.Fatal(err)
	}
	identity := func(s string) string { return s }
	token, err := r.BeginOperatorMemoryRead(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(token)
	if err != nil || string(raw) != "{}" {
		t.Fatal("serialized read authority", err)
	}
	var forged OperatorMemoryRead
	if err = json.Unmarshal(raw, &forged); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []*OperatorMemoryRead{nil, &forged} {
		if _, err = r.ReadOperatorMemoryPage(ctx, candidate, 0, identity); err == nil {
			t.Fatal("data-only read accepted")
		}
		if _, err = r.CommandPromoteReviewedMemory(ctx, candidate, "Explicit owner review"); err == nil {
			t.Fatal("data-only promotion accepted")
		}
	}
	if _, err = (&Runtime{}).ReadOperatorMemoryPage(ctx, token, 0, identity); err == nil {
		t.Fatal("foreign runtime read accepted")
	}
	if _, err = r.ReadOperatorMemoryPage(t.Context(), token, 0, identity); err == nil {
		t.Fatal("foreign owner read accepted")
	}
	if _, err = r.ReadOperatorMemoryPage(ctx, token, 0, nil); err == nil {
		t.Fatal("missing trusted display guard accepted")
	}
	token.expires = time.Now().Add(-time.Second)
	if _, err = r.ReadOperatorMemoryPage(ctx, token, 0, identity); err == nil {
		t.Fatal("expired read accepted")
	}
	token, err = r.BeginOperatorMemoryRead(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.ReadOperatorMemoryPage(ctx, token, 0, identity); err != nil {
		t.Fatal(err)
	}
	token.expires = time.Now().Add(-time.Second)
	if _, err = r.CommandPromoteReviewedMemory(ctx, token, "Explicit owner review"); err == nil {
		t.Fatal("expired complete review promoted")
	}
	token, err = r.BeginOperatorMemoryRead(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.ReadOperatorMemoryPage(ctx, token, 0, identity); err != nil {
		t.Fatal(err)
	}
	principal, err := requiredArchitectureOwner(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	current, err := r.store.GetMemoryV2(ctx, rec.ProjectID, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	mutation := context.WithValue(ctx, operatorMemoryPromotionKey{}, token)
	if err = checkOperatorMemoryPromotionRead(mutation, r.memoryService, principal, current); err != nil {
		t.Fatal("current complete review lost SQL pin", err)
	}
	for _, field := range []string{"created", "updated", "evidence", "source", "confidence"} {
		t.Run(field, func(t *testing.T) {
			changed := current
			switch field {
			case "created":
				changed.CreatedAt = changed.CreatedAt.Add(time.Second)
			case "updated":
				changed.UpdatedAt = changed.UpdatedAt.Add(time.Second)
			case "evidence":
				changed.EvidenceIDs = []string{"E-replaced"}
			case "source":
				changed.Source.Reference += " changed"
			case "confidence":
				changed.Confidence = model.ConfidenceInferred
			}
			if err := checkOperatorMemoryPromotionRead(mutation, r.memoryService, principal, changed); err == nil {
				t.Fatal("same revision accepted changed full-state pin")
			}
		})
	}
	token.Invalidate()
	if _, err = r.CommandPromoteReviewedMemory(ctx, token, "Explicit owner review"); err == nil {
		t.Fatal("invalidated full read promoted")
	}
	if _, err = r.BeginOperatorMemoryRead(ctx, strings.Repeat("x", 257)); err == nil {
		t.Fatal("oversized identity accepted")
	}
	if _, err = r.BeginOperatorMemoryRead(t.Context(), rec.ID); err == nil {
		t.Fatal("untrusted read initializer accepted")
	}
	if _, err = r.OperatorMemoryReview(t.Context(), "", 12); err == nil {
		t.Fatal("untrusted review page accepted")
	}
}

func operatorReviewedCandidate(t *testing.T, r *Runtime, source model.MemoryRecordV2, id string) (context.Context, *OperatorMemoryRead, model.MemoryRecordV2) {
	t.Helper()
	control, err := r.OpenLocalControl(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx := control.Context(t.Context())
	rec := source
	rec.ID = id
	rec.Revision = 1
	rec.Lifecycle = model.MemoryCandidate
	rec.Authority = model.AuthorityAgent
	rec.Source.Kind = "runtime_service"
	if err = r.store.WriteMemoryV2(ctx, rec); err != nil {
		t.Fatal(err)
	}
	token, err := r.BeginOperatorMemoryRead(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.ReadOperatorMemoryPage(ctx, token, 0, func(s string) string { return s }); err != nil {
		t.Fatal(err)
	}
	return ctx, token, rec
}

func TestOperatorMemoryIndexFailureRetainsCanonicalCommitAndRepairsOnlyOnce(t *testing.T) {
	r, _, source, _ := requiredContextFixture(t)
	ctx, token, rec := operatorReviewedCandidate(t, r, source, "MEM-derived-repair")
	fault := errors.New("owned derived index unavailable")
	failed := context.WithValue(ctx, operatorMemoryDerivedIndexKey{}, func(context.Context, model.MemoryRecordV2) error { return fault })
	result, err := r.CommandPromoteReviewedMemory(failed, token, "Reviewed canonical source")
	if err != nil || !result.Persisted || result.Indexed || result.Revision != 2 {
		t.Fatal("derived index fault concealed canonical commit", result, err)
	}
	canonical, err := r.store.GetMemoryV2(ctx, rec.ProjectID, rec.ID)
	if err != nil || canonical.Authority != model.AuthorityOperator || canonical.Revision != 2 {
		t.Fatal("index fault rolled back canonical promotion", err)
	}
	called := 0
	repaired := context.WithValue(ctx, operatorMemoryDerivedIndexKey{}, func(c context.Context, m model.MemoryRecordV2) error {
		called++
		return r.memoryService.IndexRecord(c, m)
	})
	if _, err = r.CommandPromoteReviewedMemory(repaired, token, "Different rationale"); err == nil || called != 0 {
		t.Fatal("repair changed canonical rationale")
	}
	result, err = r.CommandPromoteReviewedMemory(repaired, token, "Reviewed canonical source")
	if err != nil || !result.Indexed || called != 1 {
		t.Fatal("unchanged canonical index not repaired", result, err)
	}
	current, err := r.store.GetMemoryV2(ctx, rec.ProjectID, rec.ID)
	if err != nil || operatorMemoryState(current) != operatorMemoryState(canonical) {
		t.Fatal("repair repeated canonical mutation", err)
	}
	if _, err = r.CommandPromoteReviewedMemory(repaired, token, "Reviewed canonical source"); err == nil || called != 1 {
		t.Fatal("index success replayed")
	}
}

func TestOperatorMemoryPostCommitACLChangeCannotHideItsActualReceipt(t *testing.T) {
	r, _, source, _ := requiredContextFixture(t)
	ctx, token, rec := operatorReviewedCandidate(t, r, source, "MEM-actual-commit-receipt")
	changed := context.WithValue(ctx, operatorMemoryDerivedIndexKey{}, func(c context.Context, m model.MemoryRecordV2) error {
		_, err := r.store.UpdateMemory(c, m.ProjectID, m.ID, m.Revision, func(row *model.MemoryRecordV2) error { row.ACLScope = "SESSION-foreign"; return nil })
		if err != nil {
			t.Fatal(err)
		}
		return errors.New("derived index failed after another canonical write")
	})
	result, err := r.CommandPromoteReviewedMemory(changed, token, "Reviewed canonical source")
	if err == nil || !result.Persisted || result.ID != rec.ID || result.Revision != 2 || result.Indexed {
		t.Fatal("actual same-operation commit receipt hidden by later ACL change", result, err)
	}
	called := false
	retry := context.WithValue(ctx, operatorMemoryDerivedIndexKey{}, func(context.Context, model.MemoryRecordV2) error { called = true; return nil })
	if _, err = r.CommandPromoteReviewedMemory(retry, token, "Reviewed canonical source"); err == nil || called {
		t.Fatal("revoked ACL authorized index repair")
	}
}

func TestOperatorMemoryLiveReviewFiltersBeforeMetadataAndNeverGrantsRead(t *testing.T) {
	r, _, source, _ := requiredContextFixture(t)
	control, err := r.OpenLocalControl(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx := control.Context(t.Context())
	owner, err := requiredArchitectureOwner(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.store.ImportTasks(ctx, []model.Task{{ID: "TASK-live-own", Title: "Canonical live review scope", Status: model.TaskReady, Risk: model.R0}}); err != nil {
		t.Fatal(err)
	}
	r.permissionMu.Lock()
	r.continuationCandidates = map[string]model.MemoryRecordV2{}
	for _, id := range []string{"MEM-live-001", "MEM-live-002", "MEM-hidden-acl", "MEM-hidden-project", "MEM-hidden-task", "MEM-hidden-owner"} {
		rec := source
		rec.ID = id
		rec.Lifecycle = model.MemoryCandidate
		switch id {
		case "MEM-live-002":
			rec.Scope = "task"
			rec.ScopeID = "TASK-live-own"
		case "MEM-hidden-acl":
			rec.ACLScope = "foreign-reader"
		case "MEM-hidden-project":
			rec.ProjectID = "foreign-project"
		case "MEM-hidden-task":
			rec.Scope = "task"
			rec.ScopeID = "TASK-foreign"
		case "MEM-hidden-owner":
			rec.Scope = "operator-private"
			rec.ScopeID = "foreign-owner"
		}
		r.continuationCandidates[id] = rec
	}
	r.permissionMu.Unlock()
	first, err := r.OperatorMemoryLiveReview(ctx, "", 1)
	if err != nil || len(first.Entries) != 1 || first.Entries[0].ID != "MEM-live-001" || first.NextID != "MEM-live-001" {
		t.Fatal("live metadata paging disclosed foreign source", first, err)
	}
	second, err := r.OperatorMemoryLiveReview(ctx, first.NextID, 1)
	if err != nil || len(second.Entries) != 1 || second.Entries[0].ID != "MEM-live-002" || second.NextID != "" {
		t.Fatal("live task scope metadata lost canonical membership", second, err)
	}
	if _, err = r.BeginOperatorMemoryRead(ctx, first.Entries[0].ID); err == nil {
		t.Fatal("live metadata created canonical full-read capability")
	}
	if _, err = r.OperatorMemoryLiveReview(t.Context(), "", 1); err == nil {
		t.Fatal("unauthenticated live metadata disclosed")
	}
	if _, err = r.OperatorMemoryLiveReview(ctx, "", 0); err == nil {
		t.Fatal("unbounded live metadata admitted")
	}
	if owner.ID == "" {
		t.Fatal("fixture lacks actual local owner")
	}
}
