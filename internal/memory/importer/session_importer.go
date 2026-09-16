package importer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/security"
	"github.com/Zen1th53/marshal/internal/model"
)

type Config struct {
	Firewall *security.Firewall
	Adapters []HistoryAdapter
}

type Message struct {
	Role string `json:"role"`
	// Kind distinguishes conversation text from a captured tool call or tool
	// result. An empty kind is plain text, so older transcripts keep decoding.
	Kind    string `json:"kind,omitempty"`
	Content string `json:"content"`
	// Timestamp is the provider's own time for this message. Records are keyed
	// on it, so a session's messages order correctly instead of collapsing onto
	// the session's start time.
	Timestamp time.Time `json:"timestamp,omitempty"`
}

type SessionTranscript struct {
	SessionID string    `json:"session_id"`
	Provider  string    `json:"provider"`
	TaskID    string    `json:"task_id"`
	Messages  []Message `json:"messages"`
	Success   *bool     `json:"success,omitempty"`
	Timestamp time.Time `json:"timestamp,omitempty"`
	CWD       string    `json:"cwd,omitempty"`
	Branch    string    `json:"branch,omitempty"`
}

type ImportResult struct {
	ImportedRecords []model.MemoryRecordV2 `json:"imported_records"`
	SkippedCount    int                    `json:"skipped_count"`
}

type SessionImporter struct {
	firewall *security.Firewall
	adapters map[ProviderFormat]HistoryAdapter
}

func NewSessionImporter(config Config) *SessionImporter {
	fw := config.Firewall
	if fw == nil {
		fw = security.NewFirewall(security.FirewallConfig{})
	}
	imp := &SessionImporter{
		firewall: fw,
		adapters: make(map[ProviderFormat]HistoryAdapter),
	}
	for _, adapter := range []HistoryAdapter{CodexJSONLAdapter{}, ClaudeJSONLAdapter{}, GeminiJSONLAdapter{}} {
		imp.adapters[adapter.Format()] = adapter
	}
	for _, adapter := range config.Adapters {
		if adapter != nil && adapter.Format() != "" {
			imp.adapters[adapter.Format()] = adapter
		}
	}
	return imp
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

// ImportRawJSON parses the stable MARSHAL interchange format. Provider-native
// histories must first pass through ImportProviderHistory so hidden reasoning
// and tool payloads cannot accidentally become durable memory.
func (s *SessionImporter) ImportRawJSON(ctx context.Context, projectID string, data []byte, dryRun bool) (ImportResult, error) {
	if len(data) == 0 {
		return ImportResult{}, errors.New("empty transcript payload")
	}
	if len(data) > maxHistoryBytes {
		return ImportResult{}, fmt.Errorf("session transcript exceeds %d-byte limit", maxHistoryBytes)
	}

	var tr SessionTranscript
	if err := json.Unmarshal(data, &tr); err != nil {
		return ImportResult{}, fmt.Errorf("parse session transcript: %w", err)
	}
	return s.importTranscript(ctx, projectID, tr, "marshal-json", dryRun)
}

func (s *SessionImporter) importTranscript(ctx context.Context, projectID string, tr SessionTranscript, sourceFormat string, _ bool) (ImportResult, error) {
	if err := ctx.Err(); err != nil {
		return ImportResult{}, err
	}
	projectID = strings.TrimSpace(projectID)
	tr.SessionID = strings.TrimSpace(tr.SessionID)
	tr.Provider = strings.TrimSpace(tr.Provider)
	if projectID == "" {
		return ImportResult{}, errors.New("project_id is required")
	}
	if tr.SessionID == "" {
		return ImportResult{}, errors.New("session_id is required")
	}
	if tr.Provider == "" {
		return ImportResult{}, errors.New("provider is required")
	}

	var sb strings.Builder
	var latest time.Time
	for _, m := range tr.Messages {
		role := strings.ToLower(strings.TrimSpace(m.Role))
		content := strings.TrimSpace(m.Content)
		if (role != "user" && role != "assistant") || content == "" {
			continue
		}
		// Only kinds this package defines may label a record. An arbitrary
		// provider string must never reach the body as a trusted marker.
		label := role
		switch strings.ToLower(strings.TrimSpace(m.Kind)) {
		case MessageKindToolUse:
			label = role + ":" + MessageKindToolUse
		case MessageKindToolResult:
			label = role + ":" + MessageKindToolResult
		}
		if m.Timestamp.After(latest) {
			latest = m.Timestamp
		}
		sb.WriteString(fmt.Sprintf("[%s]: %s\n", label, content))
	}
	body := strings.TrimSpace(sb.String())
	if body == "" {
		return ImportResult{}, errors.New("session contains no importable messages")
	}

	// Prefer the message's own time over the session's start time; a session
	// imported in chunks would otherwise stamp every record identically and
	// lose the order in which the work actually happened.
	baseTime := tr.Timestamp.UTC()
	if !latest.IsZero() {
		baseTime = latest.UTC()
	}
	if baseTime.IsZero() {
		// A fixed fallback keeps repeat imports deterministic without claiming a
		// fabricated observation time in the current clock domain.
		baseTime = time.Unix(0, 0).UTC()
	}
	importID := generateDeterministicImportID(projectID, tr.SessionID, body)

	rec := model.MemoryRecordV2{
		ID:         importID,
		ProjectID:  projectID,
		Kind:       model.MemoryKindEpisodic,
		Lifecycle:  model.MemoryCandidate,
		Confidence: model.ConfidenceObserved,
		Authority:  model.AuthorityAgent,
		Title:      fmt.Sprintf("Imported Session %s (%s)", tr.SessionID, tr.Provider),
		Body:       body,
		Scope:      string(model.ScopeSession),
		ScopeID:    tr.SessionID,
		ObservedAt: baseTime,
		IngestedAt: baseTime,
		ValidFrom:  baseTime,
		CreatedAt:  baseTime,
		UpdatedAt:  baseTime,
		SessionID:  tr.SessionID,
		Source: model.MemorySource{
			Kind:      "external",
			Reference: tr.SessionID,
		},
		ExtMeta: map[string]any{
			"provider":             tr.Provider,
			"task_id":              tr.TaskID,
			"source_format":        sourceFormat,
			"imported_retroactive": true,
		},
	}
	if tr.Success != nil {
		rec.ExtMeta["execution_success"] = *tr.Success
	}
	if tr.CWD != "" {
		rec.ExtMeta["source_cwd"] = tr.CWD
	}
	if tr.Branch != "" {
		rec.ExtMeta["source_branch"] = tr.Branch
	}

	// Security scan
	if err := s.firewall.ScanRecord(ctx, rec); err != nil {
		return ImportResult{}, err
	}

	digest := rec.CanonicalDigest()
	rec.ContentDigest = digest
	return ImportResult{
		ImportedRecords: []model.MemoryRecordV2{rec},
		SkippedCount:    0,
	}, nil
}
