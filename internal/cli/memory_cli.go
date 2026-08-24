package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/authz"
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
	memoryService := runtime.Memory()
	operator := authz.Principal{ID: "cli-operator", Role: authz.Role{Name: "operator", Authorities: []authz.Authority{authz.AuthorityPolicyAdmin}}}

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
		return c.memoryRemember(ctx, memoryService, operator, projectID, subArgs)

	case "recall":
		query := strings.Join(subArgs, " ")
		res, err := memoryService.Recall(ctx, operator, app.RecallRequest{ProjectID: projectID, Query: query})
		if err != nil {
			return err
		}
		human := fmt.Sprintf("recall query=%q results=%d", query, len(res.Results))
		for _, item := range res.Results {
			human += fmt.Sprintf("\n%s %s %s", item.ID, item.Lifecycle, item.Title)
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
		promoted, err := memoryService.Promote(ctx, operator, app.PromoteRequest{ProjectID: projectID, MemoryID: memID, ScopeID: projectID})
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
		if err := memoryService.IndexRecord(ctx, tombstoned); err != nil {
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

func (c command) memoryRemember(ctx context.Context, service *app.MemoryService, principal authz.Principal, projectID string, args []string) error {
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
	rec, err := service.Remember(ctx, principal, app.RememberRequest{ProjectID: projectID, ScopeID: projectID, Title: title, Body: body, Kind: kind})
	if err != nil {
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
