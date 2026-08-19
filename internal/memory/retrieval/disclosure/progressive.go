package disclosure

import (
	"context"
	"errors"
	"fmt"

	"github.com/Zen1th53/marshal/internal/memory/security"
	"github.com/Zen1th53/marshal/internal/model"
)

var (
	ErrRecordRevoked        = errors.New("memory record was revoked, tombstoned, or unauthorized")
	ErrExceededCeilingLimit = errors.New("disclosure payload exceeded configured ceiling")
)

type Config struct {
	Level1ByteCap int
	Level2ByteCap int
	Level3ByteCap int
	Firewall      *security.Firewall
}

type Level1Summary struct {
	ID        string                `json:"id"`
	Title     string                `json:"title"`
	Kind      model.MemoryKind      `json:"kind"`
	Lifecycle model.MemoryLifecycle `json:"lifecycle"`
	Authority model.MemoryAuthority `json:"authority"`
	Summary   string                `json:"summary"`
}

type Level2Detail struct {
	ID          string                `json:"id"`
	Title       string                `json:"title"`
	Body        string                `json:"body"`
	Kind        model.MemoryKind      `json:"kind"`
	Lifecycle   model.MemoryLifecycle `json:"lifecycle"`
	Authority   model.MemoryAuthority `json:"authority"`
	Scope       string                `json:"scope"`
	ScopeID     string                `json:"scope_id"`
	EvidenceIDs []string              `json:"evidence_ids,omitempty"`
}

type Level3DeepExcerpt struct {
	TranscriptExcerpt string `json:"transcript_excerpt"`
}

type Engine struct {
	config   Config
	firewall *security.Firewall
}

func NewEngine(config Config) *Engine {
	if config.Level1ByteCap <= 0 {
		config.Level1ByteCap = 250
	}
	if config.Level2ByteCap <= 0 {
		config.Level2ByteCap = 3000
	}
	if config.Level3ByteCap <= 0 {
		config.Level3ByteCap = 15000
	}
	fw := config.Firewall
	if fw == nil {
		fw = security.NewFirewall(security.FirewallConfig{})
	}
	return &Engine{
		config:   config,
		firewall: fw,
	}
}

// DiscloseLevel1 produces a compact, byte-capped metadata summary of a record.
func (e *Engine) DiscloseLevel1(ctx context.Context, rec model.MemoryRecordV2) (Level1Summary, error) {
	summary := rec.Body
	if len(summary) > e.config.Level1ByteCap {
		summary = summary[:e.config.Level1ByteCap] + "..."
	}

	return Level1Summary{
		ID:        rec.ID,
		Title:     rec.Title,
		Kind:      rec.Kind,
		Lifecycle: rec.Lifecycle,
		Authority: rec.Authority,
		Summary:   summary,
	}, nil
}

// DiscloseLevel2 re-verifies ACL and current lifecycle before revealing full body and evidence links.
func (e *Engine) DiscloseLevel2(ctx context.Context, memoryID string, allowedScopeIDs []string, resolver func(id string) (model.MemoryRecordV2, bool)) (Level2Detail, error) {
	rec, ok := resolver(memoryID)
	if !ok {
		return Level2Detail{}, ErrRecordRevoked
	}

	// Recheck lifecycle
	if rec.Lifecycle == model.MemoryTombstoned || rec.Lifecycle == model.MemoryRejected {
		return Level2Detail{}, fmt.Errorf("%w: record %s is %s", ErrRecordRevoked, memoryID, rec.Lifecycle)
	}

	// Recheck scope ACL
	allowedMap := make(map[string]bool)
	for _, s := range allowedScopeIDs {
		allowedMap[s] = true
	}
	if len(allowedScopeIDs) > 0 && rec.ScopeID != "" && !allowedMap[rec.ScopeID] {
		return Level2Detail{}, fmt.Errorf("%w: scope %s unauthorized", ErrRecordRevoked, rec.ScopeID)
	}

	body := rec.Body
	if len(body) > e.config.Level2ByteCap {
		body = body[:e.config.Level2ByteCap] + "\n[TRUNCATED_AT_LEVEL2_CEILING]"
	}

	return Level2Detail{
		ID:          rec.ID,
		Title:       rec.Title,
		Body:        body,
		Kind:        rec.Kind,
		Lifecycle:   rec.Lifecycle,
		Authority:   rec.Authority,
		Scope:       rec.Scope,
		ScopeID:     rec.ScopeID,
		EvidenceIDs: rec.EvidenceIDs,
	}, nil
}

// DiscloseLevel3 exposes deep session transcript excerpts after security firewall scanning.
func (e *Engine) DiscloseLevel3(ctx context.Context, rawTranscript string) (Level3DeepExcerpt, error) {
	tempRec := model.MemoryRecordV2{
		ID:   "L3-SCAN",
		Body: rawTranscript,
	}

	if err := e.firewall.ScanRecord(ctx, tempRec); err != nil {
		return Level3DeepExcerpt{}, err
	}

	excerpt := rawTranscript
	if len(excerpt) > e.config.Level3ByteCap {
		excerpt = excerpt[:e.config.Level3ByteCap] + "\n[TRUNCATED_AT_LEVEL3_CEILING]"
	}

	return Level3DeepExcerpt{
		TranscriptExcerpt: excerpt,
	}, nil
}
