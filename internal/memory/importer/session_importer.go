package importer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/security"
	"github.com/Zen1th53/marshal/internal/model"
)

type Config struct {
	Firewall *security.Firewall
}

type SessionTranscript struct {
	SessionID string `json:"session_id"`
	Provider  string `json:"provider"`
	TaskID    string `json:"task_id"`
	Messages  []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
	Success bool `json:"success"`
}

type ImportResult struct {
	ImportedRecords []model.MemoryRecordV2 `json:"imported_records"`
	SkippedCount    int                    `json:"skipped_count"`
}

type SessionImporter struct {
	mu           sync.Mutex
	config       Config
	firewall     *security.Firewall
	seenDigests  map[string]bool
}

func NewSessionImporter(config Config) *SessionImporter {
	fw := config.Firewall
	if fw == nil {
		fw = security.NewFirewall(security.FirewallConfig{})
	}
	return &SessionImporter{
		config:      config,
		firewall:    fw,
		seenDigests: make(map[string]bool),
	}
}

func generateDeterministicImportID(projectID, sessionID, body string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%s", projectID, sessionID, body)
	sum := hex.EncodeToString(h.Sum(nil))
	if len(sum) > 16 {
		sum = sum[:16]
	}
	return fmt.Sprintf("MEM-IMPORT-%s", sum)
}

// ImportRawJSON parses, scans, and converts raw agent session transcripts into canonical episodic memory records.
func (s *SessionImporter) ImportRawJSON(ctx context.Context, projectID string, data []byte, dryRun bool) (ImportResult, error) {
	if len(data) == 0 {
		return ImportResult{}, errors.New("empty transcript payload")
	}

	var tr SessionTranscript
	if err := json.Unmarshal(data, &tr); err != nil {
		return ImportResult{}, fmt.Errorf("parse session transcript: %w", err)
	}

	// Build concatenated message summary
	var sb strings.Builder
	for _, m := range tr.Messages {
		sb.WriteString(fmt.Sprintf("[%s]: %s\n", m.Role, m.Content))
	}
	body := strings.TrimSpace(sb.String())
	if body == "" {
		body = "Session transcript imported"
	}

	// Deterministic base time for imported historical records
	baseTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	importID := generateDeterministicImportID(projectID, tr.SessionID, body)

	rec := model.MemoryRecordV2{
		ID:          importID,
		ProjectID:   projectID,
		Kind:        model.MemoryKindEpisodic,
		Lifecycle:   model.MemoryCandidate,
		Confidence:  model.ConfidenceObserved,
		Authority:   model.AuthorityAgent,
		Title:       fmt.Sprintf("Imported Session %s (%s)", tr.SessionID, tr.Provider),
		Body:        body,
		Scope:       string(model.ScopeSession),
		ScopeID:     tr.SessionID,
		ObservedAt:  baseTime,
		IngestedAt:  baseTime,
		ValidFrom:   baseTime,
		CreatedAt:   baseTime,
		UpdatedAt:   baseTime,
		SessionID:   tr.SessionID,
		Source: model.MemorySource{
			Kind:      "external",
			Reference: tr.SessionID,
		},
		ExtMeta: map[string]any{
			"provider":          tr.Provider,
			"execution_success": tr.Success,
			"imported_retroactive": true,
		},
	}

	// Security scan
	if err := s.firewall.ScanRecord(ctx, rec); err != nil {
		return ImportResult{}, err
	}

	digest := rec.CanonicalDigest()

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.seenDigests[digest] {
		return ImportResult{
			ImportedRecords: nil,
			SkippedCount:    1,
		}, nil
	}

	if !dryRun {
		s.seenDigests[digest] = true
	}

	rec.ContentDigest = digest
	return ImportResult{
		ImportedRecords: []model.MemoryRecordV2{rec},
		SkippedCount:    0,
	}, nil
}
