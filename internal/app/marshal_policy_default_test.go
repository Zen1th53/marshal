//go:build governed

package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/settingscatalog"
)

func TestGovernedReviewPolicyCannotBeDisabled(t *testing.T) {
	ctx := context.Background()
	s, _ := marshalFixture(t, 1)

	// Every review policy control in the catalogue must be locked in governed edition.
	for _, key := range []string{"cross-review", "design-review", "architecture-conformance", "final-model"} {
		d, err := settingscatalog.Find(key)
		if err != nil {
			t.Fatalf("catalogue missing %s: %v", key, err)
		}
		if d.LockReason == "" {
			t.Fatalf("governed catalogue left %s unlocked", key)
		}
	}

	// Direct catalogue setters refuse every weakened value with ErrReviewPolicyLocked.
	defaults := marshal.DefaultSettings()
	for _, attempt := range [][2]string{
		{"cross-review", "off"},
		{"design-review", "off"},
		{"architecture-conformance", "warn"},
		{"architecture-conformance", "off"},
		{"final-model", "off"},
	} {
		if _, err := settingscatalog.Apply(defaults, attempt[0], attempt[1]); !errors.Is(err, marshal.ErrReviewPolicyLocked) {
			t.Fatalf("Apply(%s=%s) = %v, want ErrReviewPolicyLocked", attempt[0], attempt[1], err)
		}
	}

	// Direct Settings.Validate and Store.CommitMarshalSettings refuse weakened policies.
	weakPolicies := []marshal.ReviewPolicy{
		{CrossReview: marshal.CrossReviewOff},
		{Design: marshal.DesignReviewOff},
		{Conformance: marshal.ConformanceWarn},
		{Conformance: marshal.ConformanceOff},
		{FinalModel: marshal.FinalModelOff},
	}
	for _, rp := range weakPolicies {
		bad := marshal.DefaultSettings()
		bad.ReviewPolicy = rp
		if err := bad.Validate(); !errors.Is(err, marshal.ErrReviewPolicyLocked) {
			t.Fatalf("Settings.Validate(%+v) = %v, want ErrReviewPolicyLocked", rp, err)
		}
		if _, _, err := s.Store.CommitMarshalSettings(ctx, s.ProjectID, bad, 0, "owner"); !errors.Is(err, marshal.ErrReviewPolicyLocked) {
			t.Fatalf("CommitMarshalSettings(%+v) = %v, want ErrReviewPolicyLocked", rp, err)
		}
		// TierPolicy fails closed even for unvalidated in-memory structs.
		tp := marshal.TierPolicy(nil, bad)
		if !tp.CrossReviewRequired || tp.ReviewPolicy.Weakened() {
			t.Fatalf("TierPolicy weakened in governed edition: %+v", tp)
		}
	}
}

func TestGovernedLocksOnDirectPresetPersistedAndResumePaths(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "state.db")
	s, _ := marshalFixture(t, 1, dbPath)

	// 1. Direct setter path refuses weakened review policy before writing.
	badSettings := marshal.DefaultSettings()
	badSettings.ReviewPolicy.CrossReview = marshal.CrossReviewOff
	if _, err := s.Store.SetMarshalSettings(ctx, s.ProjectID, badSettings, 0); !errors.Is(err, marshal.ErrReviewPolicyLocked) {
		t.Fatalf("direct SetMarshalSettings = %v, want ErrReviewPolicyLocked", err)
	}

	// 2. Preset preview and apply paths refuse weakened preset ("fast").
	if _, err := settingscatalog.PreviewPreset(marshal.DefaultSettings(), "fast"); !errors.Is(err, marshal.ErrReviewPolicyLocked) {
		t.Fatalf("PreviewPreset(fast) = %v, want ErrReviewPolicyLocked", err)
	}
	if _, _, err := settingscatalog.ApplyPreset(marshal.DefaultSettings(), "fast"); !errors.Is(err, marshal.ErrReviewPolicyLocked) {
		t.Fatalf("ApplyPreset(fast) = %v, want ErrReviewPolicyLocked", err)
	}

	// Create and approve a valid run first so we can verify legacy load and persisted-weakening rejection.
	if _, err := s.StartPlanning(ctx, "run-gov", "write", marshal.Budget{}); err != nil {
		t.Fatal(err)
	}
	approved, err := s.Approve(ctx, "run-gov")
	if err != nil {
		t.Fatal(err)
	}

	rawDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()

	// Legacy run with ReviewPolicy absent must load and resume with shipped defaults.
	legacyRun := approved
	legacyRun.ReviewPolicy = nil
	legacyRun.Settings.ReviewPolicy = marshal.ReviewPolicy{}
	legacyJSON, err := json.Marshal(legacyRun)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rawDB.ExecContext(ctx, `UPDATE marshal_runs SET data_json = ? WHERE run_id = ?`, string(legacyJSON), "run-gov"); err != nil {
		t.Fatal(err)
	}
	resumedLegacy, err := s.Resume(ctx, "run-gov")
	if err != nil {
		t.Fatalf("legacy run without ReviewPolicy failed to resume: %v", err)
	}
	if resumedLegacy.EffectiveReviewPolicy() != marshal.DefaultReviewPolicy() {
		t.Fatalf("legacy run defaults = %+v", resumedLegacy.EffectiveReviewPolicy())
	}

	// 3. Persisted weakened run snapshot must be rejected on load, resume, dispatch,
	// review, amendment, verification and close BEFORE any effects occur.
	weakRun := approved
	weakRun.ReviewPolicy = &marshal.ReviewPolicySnapshot{
		PolicyVersion: marshal.ReviewPolicyVersion,
		PlanDigest:    approved.ApprovalScopeDigest,
		FinalModel:    marshal.FinalModelRequired,
		CrossReview:   marshal.CrossReviewOff,
		Design:        marshal.DesignReviewOnDemand,
		Conformance:   marshal.ConformanceEnforce,
		Actor:         "migrated",
	}
	weakRunJSON, err := json.Marshal(weakRun)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rawDB.ExecContext(ctx, `UPDATE marshal_runs SET data_json = ? WHERE run_id = ?`, string(weakRunJSON), "run-gov"); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Store.GetMarshalRun(ctx, s.ProjectID, "run-gov"); !errors.Is(err, marshal.ErrReviewPolicyLocked) {
		t.Fatalf("GetMarshalRun persisted weakened snapshot = %v, want ErrReviewPolicyLocked", err)
	}
	if _, err := s.Resume(ctx, "run-gov"); !errors.Is(err, marshal.ErrReviewPolicyLocked) {
		t.Fatalf("Resume persisted weakened snapshot = %v, want ErrReviewPolicyLocked", err)
	}
	if _, err := s.Dispatch(ctx, "run-gov", "a", "write"); !errors.Is(err, marshal.ErrReviewPolicyLocked) {
		t.Fatalf("Dispatch persisted weakened snapshot = %v, want ErrReviewPolicyLocked", err)
	}
	if _, err := s.Review(ctx, "run-gov", "a", knownCharge()); !errors.Is(err, marshal.ErrReviewPolicyLocked) {
		t.Fatalf("Review persisted weakened snapshot = %v, want ErrReviewPolicyLocked", err)
	}
	if _, _, err := s.ProposeAmend(ctx, "run-gov", "change"); !errors.Is(err, marshal.ErrReviewPolicyLocked) {
		t.Fatalf("ProposeAmend persisted weakened snapshot = %v, want ErrReviewPolicyLocked", err)
	}
	if _, err := s.VerifyMerged(ctx, "run-gov", knownCharge()); !errors.Is(err, marshal.ErrReviewPolicyLocked) {
		t.Fatalf("VerifyMerged persisted weakened snapshot = %v, want ErrReviewPolicyLocked", err)
	}
	if err := s.Close(ctx, "run-gov"); !errors.Is(err, marshal.ErrReviewPolicyLocked) {
		t.Fatalf("Close persisted weakened snapshot = %v, want ErrReviewPolicyLocked", err)
	}

	// 4. Persisted weakened project settings must be rejected on load and before
	// starting a new plan or binding an approved plan.
	weakSettingsJSON, err := json.Marshal(badSettings)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rawDB.ExecContext(ctx, `INSERT OR REPLACE INTO marshal_settings(project_id, revision, data_json) VALUES(?, 1, ?)`, s.ProjectID, string(weakSettingsJSON)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.GetMarshalSettings(ctx, s.ProjectID); !errors.Is(err, marshal.ErrReviewPolicyLocked) {
		t.Fatalf("GetMarshalSettings persisted weakened = %v, want ErrReviewPolicyLocked", err)
	}
	if _, err := s.StartPlanning(ctx, "run-blocked", "write", marshal.Budget{}); !errors.Is(err, marshal.ErrReviewPolicyLocked) {
		t.Fatalf("StartPlanning with persisted weakened settings = %v, want ErrReviewPolicyLocked", err)
	}
}
