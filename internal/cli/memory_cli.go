package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

func (c command) memory(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: memory subcommand required (status, remember, recall, show, list, promote, tombstone, audit)", model.ErrInvalid)
	}

	sub := args[0]
	subArgs := args[1:]

	runtime, err := app.Open(ctx, c.root)
	if err != nil {
		return err
	}
	defer runtime.Close()
	st := runtime.Store()

	projectID, err := currentProjectID(ctx, st)
	if err != nil {
		return err
	}

	switch sub {
	case "status":
		version, err := st.SchemaVersion(ctx)
		if err != nil {
			return err
		}
		records, err := st.ListMemoryV2(ctx, store.MemoryQueryFilter{ProjectID: projectID})
		if err != nil {
			return err
		}
		status := map[string]any{
			"version":        "2.0.0",
			"healthy":        true,
			"schema_version": version,
			"records":        len(records),
			"project_id":     projectID,
		}
		return c.print(status, fmt.Sprintf("status=healthy version=2.0.0 records=%d schema=v%d", len(records), version))

	case "remember", "write":
		return c.memoryRemember(ctx, st, projectID, subArgs)

	case "recall":
		query := strings.Join(subArgs, " ")
		records, err := st.ListMemoryV2(ctx, store.MemoryQueryFilter{ProjectID: projectID})
		if err != nil {
			return err
		}
		results := filterMemoryByQuery(records, query)
		res := map[string]any{"query": query, "results": results}
		human := fmt.Sprintf("recall query=%q results=%d", query, len(results))
		for _, r := range results {
			human += fmt.Sprintf("\n%s %s %s", r["id"], r["lifecycle"], r["title"])
		}
		return c.print(res, human)

	case "show":
		if len(subArgs) == 0 {
			return fmt.Errorf("%w: memory show requires a memory ID", model.ErrInvalid)
		}
		rec, err := st.GetMemoryV2(ctx, projectID, subArgs[0])
		if err != nil {
			return err
		}
		return c.print(rec, fmt.Sprintf("%s %s %s", rec.ID, rec.Lifecycle, rec.Title))

	case "list":
		records, err := st.ListMemoryV2(ctx, store.MemoryQueryFilter{ProjectID: projectID})
		if err != nil {
			return err
		}
		res := map[string]any{"items": records}
		human := fmt.Sprintf("memory list count=%d", len(records))
		for _, r := range records {
			human += fmt.Sprintf("\n%s %s %s", r.ID, r.Lifecycle, r.Title)
		}
		return c.print(res, human)

	case "promote":
		memID, dryRun := parseMemoryID(subArgs)
		if memID == "" {
			return fmt.Errorf("%w: memory promote requires a memory ID", model.ErrInvalid)
		}
		if dryRun {
			return c.print(map[string]any{"action": "memory.promote", "memory_id": memID, "dry_run": true, "status": "DRY_RUN"}, fmt.Sprintf("promote dry-run memory_id=%s", memID))
		}
		rec, err := st.GetMemoryV2(ctx, projectID, memID)
		if err != nil {
			return err
		}
		promoted, err := st.UpdateMemory(ctx, projectID, memID, rec.Revision, func(m *model.MemoryRecordV2) error {
			m.Lifecycle = model.MemoryDurable
			m.Authority = model.AuthorityOperator
			m.UpdatedAt = time.Now().UTC()
			return nil
		})
		if err != nil {
			return err
		}
		return c.print(map[string]any{"action": "memory.promote", "memory_id": memID, "status": "PROMOTED_OK", "revision": promoted.Revision}, fmt.Sprintf("promoted memory_id=%s revision=%d", memID, promoted.Revision))

	case "tombstone":
		memID, dryRun := parseMemoryID(subArgs)
		if memID == "" {
			return fmt.Errorf("%w: memory tombstone requires a memory ID", model.ErrInvalid)
		}
		if dryRun {
			return c.print(map[string]any{"action": "memory.tombstone", "memory_id": memID, "dry_run": true, "status": "DRY_RUN"}, fmt.Sprintf("tombstone dry-run memory_id=%s", memID))
		}
		rec, err := st.GetMemoryV2(ctx, projectID, memID)
		if err != nil {
			return err
		}
		tombstoned, err := st.TombstoneMemory(ctx, projectID, memID, rec.Revision, "operator tombstone via CLI")
		if err != nil {
			return err
		}
		return c.print(map[string]any{"action": "memory.tombstone", "memory_id": memID, "status": "TOMBSTONED_OK", "revision": tombstoned.Revision}, fmt.Sprintf("tombstoned memory_id=%s revision=%d", memID, tombstoned.Revision))

	case "audit":
		records, err := st.ListMemoryV2(ctx, store.MemoryQueryFilter{ProjectID: projectID})
		if err != nil {
			return err
		}
		res := map[string]any{"subcommand": "audit", "status": "OK", "items": records}
		human := fmt.Sprintf("memory audit count=%d", len(records))
		for _, r := range records {
			human += fmt.Sprintf("\n%s %s digest=%s", r.ID, r.Lifecycle, r.ContentDigest)
		}
		return c.print(res, human)

	default:
		return fmt.Errorf("%w: unknown memory subcommand %q", model.ErrInvalid, sub)
	}
}

func currentProjectID(ctx context.Context, st *store.Store) (string, error) {
	project, err := st.Project(ctx)
	if err != nil {
		return "", err
	}
	return project.ID, nil
}

func (c command) memoryRemember(ctx context.Context, st *store.Store, projectID string, args []string) error {
	title := ""
	body := ""
	kind := model.MemoryKindSemantic
	seenKind := false
	var rest []string
	for _, a := range args {
		if strings.HasPrefix(a, "--kind=") {
			kind = model.MemoryKind(strings.TrimPrefix(a, "--kind="))
			seenKind = true
			continue
		}
		rest = append(rest, a)
	}
	if len(rest) < 2 {
		return fmt.Errorf("%w: memory remember requires TITLE and BODY arguments", model.ErrInvalid)
	}
	title = rest[0]
	body = strings.Join(rest[1:], " ")
	if !kind.IsValid() {
		if seenKind {
			return fmt.Errorf("%w: invalid memory kind %q", model.ErrInvalid, kind)
		}
		kind = model.MemoryKindSemantic
	}
	now := time.Now().UTC()
	id, err := model.NewID("MEM-")
	if err != nil {
		return err
	}
	rec := model.MemoryRecordV2{
		ID:         id,
		ProjectID:  projectID,
		Kind:       kind,
		Lifecycle:  model.MemoryCandidate,
		Confidence: model.ConfidenceObserved,
		Authority:  model.AuthorityOperator,
		Title:      title,
		Body:       body,
		Scope:      string(model.ScopeProject),
		ScopeID:    projectID,
		ObservedAt: now,
		IngestedAt: now,
		ValidFrom:  now,
		CreatedAt:  now,
		UpdatedAt:  now,
		Source:     model.MemorySource{Kind: "user", Reference: "cli"},
	}
	if err := st.WriteMemoryV2(ctx, rec); err != nil {
		return err
	}
	return c.print(map[string]any{"id": rec.ID, "status": "REMEMBERED", "lifecycle": rec.Lifecycle}, fmt.Sprintf("remembered memory_id=%s", rec.ID))
}

func parseMemoryID(args []string) (string, bool) {
	memID := ""
	dryRun := false
	for _, a := range args {
		if a == "--dry-run" {
			dryRun = true
		} else if !strings.HasPrefix(a, "-") && memID == "" {
			memID = a
		}
	}
	return memID, dryRun
}

func filterMemoryByQuery(records []model.MemoryRecordV2, query string) []map[string]any {
	q := strings.ToLower(strings.TrimSpace(query))
	results := []map[string]any{}
	for _, rec := range records {
		if rec.Lifecycle == model.MemoryTombstoned {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(rec.Title), q) && !strings.Contains(strings.ToLower(rec.Body), q) {
			continue
		}
		results = append(results, map[string]any{
			"id":        rec.ID,
			"title":     rec.Title,
			"kind":      rec.Kind,
			"lifecycle": rec.Lifecycle,
			"authority": rec.Authority,
		})
	}
	return results
}
