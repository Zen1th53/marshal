package app

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

// architectureDesign reads the design review records of the session's
// active goal for the browser: the latest brief, every review's findings
// and every resolution. Records are memory candidates and shown as data.
func (r *Runtime) architectureDesign(sessionID string) func(context.Context) (json.RawMessage, error) {
	if sessionID == "" {
		return nil
	}
	return func(ctx context.Context) (json.RawMessage, error) {
		goal, err := r.store.GetActiveGoalContract(ctx, sessionID)
		if errors.Is(err, model.ErrGoalNotFound) {
			return json.RawMessage(`{}`), nil
		}
		if err != nil {
			return nil, err
		}
		type designRecord struct {
			stage, provider, content string
			created                  time.Time
		}
		var records []designRecord
		for _, kind := range []model.MemoryKind{model.MemoryKindDecision, model.MemoryKindFinding} {
			rows, err := r.store.ListMemoryV2(ctx, store.MemoryQueryFilter{ProjectID: r.ProjectID(), Kind: kind, ScopeID: r.ProjectID(), Limit: 200})
			if err != nil {
				return nil, err
			}
			for _, row := range rows {
				if row.Source.Kind != "egm" || row.CreatedAt.Before(goal.CreatedAt) {
					continue
				}
				var body struct {
					Provenance struct {
						Stage, Provider string
					} `json:"provenance"`
					Content string `json:"content"`
				}
				if err := json.Unmarshal([]byte(row.Body), &body); err != nil {
					continue
				}
				records = append(records, designRecord{stage: body.Provenance.Stage, provider: body.Provenance.Provider, content: body.Content, created: row.CreatedAt})
			}
		}
		var brief *designRecord
		for i := range records {
			if records[i].stage == "concepts_brief" && (brief == nil || records[i].created.After(brief.created)) {
				brief = &records[i]
			}
		}
		threshold := goal.CreatedAt
		if brief != nil {
			threshold = brief.created
		}
		var findings, resolutions []designRecord
		for _, record := range records {
			if record.created.Before(threshold) {
				continue
			}
			switch record.stage {
			case "review_findings":
				findings = append(findings, record)
			case "resolution":
				resolutions = append(resolutions, record)
			}
		}
		sort.SliceStable(findings, func(i, j int) bool { return findings[i].created.Before(findings[j].created) })
		sort.SliceStable(resolutions, func(i, j int) bool { return resolutions[i].created.Before(resolutions[j].created) })
		output := map[string]any{}
		if brief != nil {
			output["brief"] = map[string]any{"provider": brief.provider, "text": brief.content}
		}
		findingItems := []any{}
		for _, record := range findings {
			findingItems = append(findingItems, map[string]any{"provider": record.provider, "text": record.content})
		}
		output["findings"] = findingItems
		resolutionItems := []any{}
		for _, record := range resolutions {
			resolutionItems = append(resolutionItems, map[string]any{"text": record.content})
		}
		output["resolutions"] = resolutionItems
		return json.Marshal(output)
	}
}
