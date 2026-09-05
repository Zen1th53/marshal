package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// SaveHarnessProfile persists probed capability intelligence for a native harness.
func (s *Store) SaveHarnessProfile(ctx context.Context, p model.HarnessProfile) error {
	if err := p.Validate(); err != nil {
		return fmt.Errorf("%w: %v", model.ErrInvalid, err)
	}

	modelsJSON, err := json.Marshal(p.SupportedModels)
	if err != nil {
		return fmt.Errorf("%w: marshal supported models: %v", model.ErrInvalid, err)
	}
	featuresJSON, err := json.Marshal(p.FeatureSupport)
	if err != nil {
		return fmt.Errorf("%w: marshal feature support: %v", model.ErrInvalid, err)
	}
	reasoningJSON, err := json.Marshal(p.ReasoningKnobs)
	if err != nil {
		return fmt.Errorf("%w: marshal reasoning knobs: %v", model.ErrInvalid, err)
	}
	modesJSON, err := json.Marshal(p.NativeModes)
	if err != nil {
		return fmt.Errorf("%w: marshal native modes: %v", model.ErrInvalid, err)
	}

	for attempt := 0; ; attempt++ {
		err = s.saveHarnessProfileTx(ctx, p, string(modelsJSON), string(featuresJSON), string(reasoningJSON), string(modesJSON))
		if !isSQLiteBusy(err) || attempt >= sqliteBusyRetries {
			return err
		}
		s.observeContention()
		if err := waitSQLiteRetry(ctx, attempt); err != nil {
			return err
		}
	}
}

func (s *Store) saveHarnessProfileTx(
	ctx context.Context,
	p model.HarnessProfile,
	modelsJSON, featuresJSON, reasoningJSON, modesJSON string,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin save harness profile: %w", err)
	}
	defer tx.Rollback()

	probedAt := p.ProbedAt.UTC().Format(time.RFC3339Nano)
	expiresAt := ""
	if !p.ExpiresAt.IsZero() {
		expiresAt = p.ExpiresAt.UTC().Format(time.RFC3339Nano)
	}

	query := `
		INSERT INTO harness_profiles (
			harness_name, installed_version, binary_path, supported_models_json,
			default_model, feature_support_json, reasoning_knobs_json, native_modes_json,
			probe_evidence_id, probed_at, expires_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(harness_name) DO UPDATE SET
			installed_version = excluded.installed_version,
			binary_path = excluded.binary_path,
			supported_models_json = excluded.supported_models_json,
			default_model = excluded.default_model,
			feature_support_json = excluded.feature_support_json,
			reasoning_knobs_json = excluded.reasoning_knobs_json,
			native_modes_json = excluded.native_modes_json,
			probe_evidence_id = excluded.probe_evidence_id,
			probed_at = excluded.probed_at,
			expires_at = excluded.expires_at
	`
	if _, err := tx.ExecContext(ctx, query,
		p.Harness,
		p.InstalledVersion,
		p.BinaryPath,
		modelsJSON,
		p.DefaultModel,
		featuresJSON,
		reasoningJSON,
		modesJSON,
		p.ProbeEvidenceID,
		probedAt,
		expiresAt,
	); err != nil {
		return fmt.Errorf("insert harness profile: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit save harness profile: %w", err)
	}
	return nil
}

// GetHarnessProfile retrieves the capability intelligence for a specific harness.
func (s *Store) GetHarnessProfile(ctx context.Context, harnessName string) (*model.HarnessProfile, error) {
	query := `
		SELECT
			harness_name, installed_version, binary_path, supported_models_json,
			default_model, feature_support_json, reasoning_knobs_json, native_modes_json,
			probe_evidence_id, probed_at, expires_at
		FROM harness_profiles
		WHERE harness_name = ?
	`
	row := s.db.QueryRowContext(ctx, query, harnessName)

	var (
		p             model.HarnessProfile
		modelsJSON    string
		featuresJSON  string
		reasoningJSON string
		modesJSON     string
		probedAt      string
		expiresAt     string
	)

	err := row.Scan(
		&p.Harness,
		&p.InstalledVersion,
		&p.BinaryPath,
		&modelsJSON,
		&p.DefaultModel,
		&featuresJSON,
		&reasoningJSON,
		&modesJSON,
		&p.ProbeEvidenceID,
		&probedAt,
		&expiresAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: harness profile not found", model.ErrNotFound)
		}
		return nil, fmt.Errorf("query harness profile: %w", err)
	}

	if modelsJSON != "" && modelsJSON != "[]" {
		_ = json.Unmarshal([]byte(modelsJSON), &p.SupportedModels)
	}
	if featuresJSON != "" && featuresJSON != "{}" {
		_ = json.Unmarshal([]byte(featuresJSON), &p.FeatureSupport)
	}
	if reasoningJSON != "" && reasoningJSON != "[]" {
		_ = json.Unmarshal([]byte(reasoningJSON), &p.ReasoningKnobs)
	}
	if modesJSON != "" && modesJSON != "[]" {
		_ = json.Unmarshal([]byte(modesJSON), &p.NativeModes)
	}

	parsedProbed, err := time.Parse(time.RFC3339Nano, probedAt)
	if err != nil {
		parsedProbed, err = time.Parse(time.RFC3339, probedAt)
		if err != nil {
			return nil, fmt.Errorf("parse probed_at: %w", err)
		}
	}
	p.ProbedAt = parsedProbed

	if expiresAt != "" {
		parsedExpires, err := time.Parse(time.RFC3339Nano, expiresAt)
		if err != nil {
			parsedExpires, _ = time.Parse(time.RFC3339, expiresAt)
		}
		p.ExpiresAt = parsedExpires
	}

	return &p, nil
}
