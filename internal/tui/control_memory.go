package tui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/memory/working"
	"github.com/Zen1th53/marshal/internal/model"
)

// MemoryActionAuthority is optional. Old/remote authorities do not acquire
// operator powers just by implementing the broad ControlAuthority interface.
type MemoryActionAuthority interface {
	ReviewMemoryActionRecord(context.Context, string) (model.MemoryRecordV2, error)
	PromoteMemoryAction(context.Context, app.CommandEnvelope, app.PromoteRequest) (model.MemoryRecordV2, error)
	ReviewMemoryActionFile(context.Context, string) (app.MemoryFileBinding, error)
	ImportMemoryAction(context.Context, app.CommandEnvelope, app.MemoryImportRequest) (app.MemoryImportReadback, error)
	ReviewMemoryActionExport(context.Context, string, string) (app.MemoryFileBinding, error)
	ExportMemoryAction(context.Context, app.CommandEnvelope, app.MemoryExportRequest) (app.MemoryFileBinding, error)
	ReviewMemoryActionSlot(context.Context, string, working.SlotType) (app.SlotPromotionReview, error)
	PromoteMemoryActionSlot(context.Context, app.CommandEnvelope, app.SlotPromotionReview, model.MemoryKind, string) (model.MemoryRecordV2, error)
}
type MemoryRestoreAuthority interface {
	ReviewMemoryActionRestore(context.Context, string) (app.MemoryRestoreReview, error)
	RestoreMemoryAction(context.Context, app.CommandEnvelope, app.MemoryRestoreRequest) (model.MemoryRestoreResult, error)
	CreateMemorySnapshotAction(context.Context, app.CommandEnvelope, string, []string) (model.MemoryRestoreSnapshot, error)
	ListMemoryRestoreSnapshots(context.Context) ([]model.MemoryRestoreSnapshot, error)
}

func memoryActionBindings(s *ControlSource) map[ActionID]Binding {
	out := map[ActionID]Binding{}
	out["CTUI-0450"] = memoryPromotionBinding(s)
	out["CTUI-0438"] = memorySlotPromotionBinding(s)
	for id, spec := range map[ActionID]struct{ format, title string }{
		"CTUI-0484": {"marshal-json", "Import MARSHAL session transcript"},
		"CTUI-0485": {"codex-jsonl", "Import Codex JSONL"},
		"CTUI-0486": {"claude-jsonl", "Import Claude JSONL"},
	} {
		out[id] = memoryImportBinding(s, id, spec.format, spec.title)
	}
	out["CTUI-0489"] = memoryExportBinding(s)
	out["CTUI-0491"] = memoryPortableBinding(s)
	out["CTUI-0071"] = memoryRestoreBinding(s)
	for id, b := range out {
		_, memoryOK := s.Authority.(MemoryActionAuthority)
		_, restoreOK := s.Authority.(MemoryRestoreAuthority)
		if id == "CTUI-0071" && !restoreOK || id != "CTUI-0071" && !memoryOK {
			b.Requires = "authenticated local memory action authority is unavailable"
			b.Execute = nil
			out[id] = b
		}
	}
	return out
}
func memoryBindingDigest(v any) string {
	body, _ := json.Marshal(v)
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
func memoryEnvelope(s *ControlSource, req ActionRequest) app.CommandEnvelope {
	return app.CommandEnvelope{ProjectID: s.ProjectID, SessionID: s.SessionID, TargetID: req.Target.ID, ExpectedVersion: req.Target.Revision, IdempotencyKey: req.IdempotencyKey}
}
func memoryAuthority(s *ControlSource) (MemoryActionAuthority, error) {
	a, ok := s.Authority.(MemoryActionAuthority)
	if !ok {
		return nil, ErrNoBinding
	}
	return a, nil
}
func memoryPromotionBinding(s *ControlSource) Binding {
	return Binding{Action: "CTUI-0450", Title: "Evidence-gated promotion", Safety: SafetyGoverned, Inputs: []InputSpec{{Key: "memory_id", Label: "Exact memory ID", Required: true}, {Key: "reason", Label: "Promotion rationale", Required: true}}, PrepareRequest: func(ctx context.Context, req ActionRequest) (Target, error) {
		a, err := memoryAuthority(s)
		if err != nil {
			return Target{}, err
		}
		rec, err := a.ReviewMemoryActionRecord(ctx, req.Inputs["memory_id"])
		if err != nil {
			return Target{}, err
		}
		return Target{Kind: "memory", ID: rec.ID, Revision: rec.Revision, Digest: rec.ContentDigest, Scope: rec.Scope + ":" + rec.ScopeID, Summary: fmt.Sprintf("Promote reviewed candidate %s @%d in %s", rec.ID, rec.Revision, rec.ScopeID)}, nil
	}, Execute: func(ctx context.Context, req ActionRequest) (Outcome, error) {
		a, err := memoryAuthority(s)
		if err != nil {
			return Outcome{}, err
		}
		rec, err := a.ReviewMemoryActionRecord(ctx, req.Target.ID)
		if err != nil {
			return refusal("read memory", err, req.Target, "MemoryService.Get")
		}
		expected := req.Target.Revision
		after, err := a.PromoteMemoryAction(ctx, memoryEnvelope(s, req), app.PromoteRequest{ProjectID: s.ProjectID, MemoryID: req.Target.ID, ScopeID: rec.ScopeID, Rationale: req.Inputs["reason"], ExpectedRevision: &expected, ExpectedDigest: req.Target.Digest})
		if err != nil {
			return refusal("promote memory", err, req.Target, "Runtime.CommandPromoteMemory")
		}
		if after.ID != req.Target.ID || after.Revision != expected+1 || after.Lifecycle != model.MemoryDurable {
			return unproven(req.Target, req.Target.ID, ErrStaleTarget)
		}
		return memorySuccess(req.Target, fmt.Sprintf("Memory %s is durable at revision %d", after.ID, after.Revision), after.ContentDigest, "Runtime.CommandPromoteMemory"), nil
	}}
}
func memoryImportBinding(s *ControlSource, id ActionID, format, title string) Binding {
	return Binding{Action: id, Title: title, Safety: SafetyPlain, Inputs: []InputSpec{{Key: "path", Label: "Project-relative input file", Required: true}, {Key: "dry_run", Label: "Dry run (true/false)", Default: "true", Required: true, Validate: validateMemoryBool}}, PrepareRequest: func(ctx context.Context, req ActionRequest) (Target, error) {
		return prepareMemoryFileImport(ctx, s, req, format)
	}, Execute: func(ctx context.Context, req ActionRequest) (Outcome, error) {
		return executeMemoryFileImport(ctx, s, req, format)
	}}
}
func prepareMemoryFileImport(ctx context.Context, s *ControlSource, req ActionRequest, format string) (Target, error) {
	a, err := memoryAuthority(s)
	if err != nil {
		return Target{}, err
	}
	file, err := a.ReviewMemoryActionFile(ctx, req.Inputs["path"])
	if err != nil {
		return Target{}, err
	}
	return Target{Kind: "memory-import", ID: file.Digest, Digest: memoryBindingDigest(struct {
		File                 app.MemoryFileBinding
		Format, Dry, Project string
	}{file, format, req.Inputs["dry_run"], s.ProjectID}), Scope: "untrusted candidates in project " + s.ProjectID, Summary: fmt.Sprintf("%s: %d bytes #%s; format %s; dry-run %s", file.Path, file.Size, shortDigest(file.Digest), format, req.Inputs["dry_run"])}, nil
}
func executeMemoryFileImport(ctx context.Context, s *ControlSource, req ActionRequest, format string) (Outcome, error) {
	a, err := memoryAuthority(s)
	if err != nil {
		return Outcome{}, err
	}
	target, err := prepareMemoryFileImport(ctx, s, req, format)
	if err != nil {
		return refusal("review import", err, req.Target, "Runtime.ReviewMemoryImport")
	}
	if target.ID != req.Target.ID || target.Digest != req.Target.Digest {
		return refusal("import file changed", ErrStaleTarget, req.Target, "Runtime.CommandImportMemory")
	}
	file, err := a.ReviewMemoryActionFile(ctx, req.Inputs["path"])
	if err != nil {
		return Outcome{}, err
	}
	dry, err := strconv.ParseBool(req.Inputs["dry_run"])
	if err != nil {
		return Outcome{}, err
	}
	result, err := a.ImportMemoryAction(ctx, memoryEnvelope(s, req), app.MemoryImportRequest{File: file, Format: format, DryRun: dry})
	if err != nil {
		return refusal("import candidates", err, req.Target, "Runtime.CommandImportMemory")
	}
	detail := fmt.Sprintf("%d candidate records; %d duplicates skipped; %s", len(result.Records), result.Skipped, result.ProjectionStatus)
	if dry {
		detail = "Dry run: " + detail
	}
	return memorySuccess(req.Target, detail, result.File.Digest, "Runtime.CommandImportMemory"), nil
}
func memoryExportBinding(s *ControlSource) Binding {
	return Binding{Action: "CTUI-0489", Title: "Export learning bundle", Safety: SafetyPlain, Inputs: []InputSpec{{Key: "path", Label: "New project-relative export file", Required: true}}, PrepareRequest: func(ctx context.Context, req ActionRequest) (Target, error) {
		return prepareMemoryFileExport(ctx, s, req, "learning")
	}, Execute: func(ctx context.Context, req ActionRequest) (Outcome, error) {
		return executeMemoryFileExport(ctx, s, req, "learning")
	}}
}
func prepareMemoryFileExport(ctx context.Context, s *ControlSource, req ActionRequest, format string) (Target, error) {
	a, err := memoryAuthority(s)
	if err != nil {
		return Target{}, err
	}
	file, err := a.ReviewMemoryActionExport(ctx, req.Inputs["path"], format)
	if err != nil {
		return Target{}, err
	}
	return Target{Kind: "memory-export", ID: file.Path, Digest: file.Digest, Scope: "public project " + s.ProjectID, Summary: fmt.Sprintf("Export %s (%d bytes) to new file %s #%s", format, file.Size, file.Path, shortDigest(file.Digest))}, nil
}
func executeMemoryFileExport(ctx context.Context, s *ControlSource, req ActionRequest, format string) (Outcome, error) {
	a, err := memoryAuthority(s)
	if err != nil {
		return Outcome{}, err
	}
	file, err := a.ExportMemoryAction(ctx, memoryEnvelope(s, req), app.MemoryExportRequest{Path: req.Target.ID, Format: format, ExpectedDigest: req.Target.Digest})
	if err != nil {
		return refusal("export memory", err, req.Target, "Runtime.CommandExportMemory")
	}
	if file.Digest != req.Target.Digest {
		return unproven(req.Target, req.Target.ID, ErrStaleTarget)
	}
	return memorySuccess(req.Target, fmt.Sprintf("Export reread: %s, %d bytes #%s", file.Path, file.Size, shortDigest(file.Digest)), file.Digest, "Runtime.CommandExportMemory"), nil
}
func memoryPortableBinding(s *ControlSource) Binding {
	return Binding{Action: "CTUI-0491", Title: "Local portable memory-pack import/export", Safety: SafetyGoverned, Inputs: []InputSpec{{Key: "operation", Label: "Operation (import/export)", Required: true, Validate: func(v string) error {
		if v != "import" && v != "export" {
			return fmt.Errorf("choose import or export")
		}
		return nil
	}}, {Key: "path", Label: "Project-relative pack path", Required: true}, {Key: "dry_run", Label: "Import dry run (true/false)", Default: "true", Required: true, Validate: validateMemoryBool}}, PrepareRequest: func(ctx context.Context, req ActionRequest) (Target, error) {
		if req.Inputs["operation"] == "import" {
			return prepareMemoryFileImport(ctx, s, req, "portable")
		}
		if req.Inputs["operation"] == "export" {
			return prepareMemoryFileExport(ctx, s, req, "portable")
		}
		return Target{}, model.ErrInvalid
	}, Execute: func(ctx context.Context, req ActionRequest) (Outcome, error) {
		if req.Inputs["operation"] == "import" {
			return executeMemoryFileImport(ctx, s, req, "portable")
		}
		if req.Inputs["operation"] == "export" {
			return executeMemoryFileExport(ctx, s, req, "portable")
		}
		return Outcome{}, model.ErrInvalid
	}}
}
func memorySlotPromotionBinding(s *ControlSource) Binding {
	return Binding{Action: "CTUI-0438", Title: "Promote slot to governed candidate", Safety: SafetyGoverned, Inputs: []InputSpec{{Key: "task_id", Label: "Exact task ID", Required: true}, {Key: "slot_type", Label: "Exact working slot type", Required: true}, {Key: "kind", Label: "Candidate kind", Default: "semantic", Required: true, Validate: func(v string) error {
		if !model.MemoryKind(v).IsValid() {
			// Name the accepted values; model.ErrInvalid alone renders as
			// "invalid input" in the form (e2e 2026-11-06).
			return fmt.Errorf("enter a memory kind such as semantic, procedural, decision or finding: %w", model.ErrInvalid)
		}
		return nil
	}}, {Key: "title", Label: "Candidate title", Required: true}}, PrepareRequest: func(ctx context.Context, req ActionRequest) (Target, error) {
		a, err := memoryAuthority(s)
		if err != nil {
			return Target{}, err
		}
		review, err := a.ReviewMemoryActionSlot(ctx, req.Inputs["task_id"], working.SlotType(req.Inputs["slot_type"]))
		if err != nil {
			return Target{}, err
		}
		return Target{Kind: "memory-slot", ID: review.SourceID, Revision: review.Revision, Digest: review.Digest, Scope: "task " + review.TaskID, Summary: fmt.Sprintf("Slot %s @%d to untrusted %s candidate", review.SlotType, review.Revision, req.Inputs["kind"])}, nil
	}, Execute: func(ctx context.Context, req ActionRequest) (Outcome, error) {
		a, err := memoryAuthority(s)
		if err != nil {
			return Outcome{}, err
		}
		review := app.SlotPromotionReview{TaskID: req.Inputs["task_id"], SlotType: working.SlotType(req.Inputs["slot_type"]), SourceID: req.Target.ID, Revision: req.Target.Revision, Digest: req.Target.Digest}
		after, err := a.PromoteMemoryActionSlot(ctx, memoryEnvelope(s, req), review, model.MemoryKind(req.Inputs["kind"]), req.Inputs["title"])
		if err != nil {
			return refusal("promote slot", err, req.Target, "MemoryService.PromoteTaskSlot")
		}
		if after.Lifecycle != model.MemoryCandidate || after.Authority != model.AuthorityAgent {
			return unproven(req.Target, after.ID, ErrStaleTarget)
		}
		return memorySuccess(req.Target, "Created candidate "+after.ID+"; durable promotion requires separate review", after.ContentDigest, "MemoryService.PromoteTaskSlot"), nil
	}}
}
func memoryRestoreBinding(s *ControlSource) Binding {
	return Binding{Action: "CTUI-0071", Title: "Roll back memory state", Safety: SafetyDestructive, Inputs: []InputSpec{{Key: "snapshot_id", Label: "Exact durable memory snapshot ID", Required: false}, {Key: "reason", Label: "Restore rationale", Required: true}}, PrepareRequest: func(ctx context.Context, req ActionRequest) (Target, error) {
		a, ok := s.Authority.(MemoryRestoreAuthority)
		if !ok {
			return Target{}, ErrNoBinding
		}
		snapshotID := strings.TrimSpace(req.Inputs["snapshot_id"])
		if snapshotID != "" && snapshotID != "latest" && snapshotID != "create" && snapshotID != "new" {
			review, err := a.ReviewMemoryActionRestore(ctx, snapshotID)
			if err != nil {
				return Target{}, err
			}
			return memoryRestoreTarget(review), nil
		}

		createRequested := snapshotID == "create" || snapshotID == "new"
		snapshots, err := a.ListMemoryRestoreSnapshots(ctx)
		if err != nil {
			return Target{}, err
		}

		if createRequested || len(snapshots) == 0 {
			name := fmt.Sprintf("snap-%d", time.Now().Unix())
			envelope := memoryEnvelope(s, req)
			if envelope.TargetID == "" {
				envelope.TargetID = fmt.Sprintf("SNAP-%s-%d", s.ProjectID, time.Now().UnixNano())
			}
			if envelope.IdempotencyKey == "" {
				envelope.IdempotencyKey = fmt.Sprintf("create-snap-%d", time.Now().UnixNano())
			}
			snap, err := a.CreateMemorySnapshotAction(ctx, envelope, name, nil)
			if err != nil {
				if len(snapshots) == 0 {
					return Target{}, fmt.Errorf("no memory snapshots available and creation failed: %w", err)
				}
				return Target{}, err
			}
			review, err := a.ReviewMemoryActionRestore(ctx, snap.ID)
			if err != nil {
				return Target{}, err
			}
			return memoryRestoreTarget(review), nil
		}

		latest := snapshots[0]
		for _, item := range snapshots {
			if item.CreatedAt.After(latest.CreatedAt) {
				latest = item
			}
		}
		review, err := a.ReviewMemoryActionRestore(ctx, latest.ID)
		if err != nil {
			return Target{}, err
		}
		return memoryRestoreTarget(review), nil
	}, Execute: func(ctx context.Context, req ActionRequest) (Outcome, error) {
		a, ok := s.Authority.(MemoryRestoreAuthority)
		if !ok {
			return Outcome{}, ErrNoBinding
		}
		review, err := a.ReviewMemoryActionRestore(ctx, req.Target.ID)
		if err != nil {
			return Outcome{}, err
		}
		current := memoryRestoreTarget(review)
		if current.Digest != req.Target.Digest || current.Revision != req.Target.Revision {
			return refusal("memory state changed", ErrStaleTarget, req.Target, "Runtime.CommandRestoreMemory")
		}
		result, err := a.RestoreMemoryAction(ctx, memoryEnvelope(s, req), app.MemoryRestoreRequest{SnapshotID: review.Snapshot.ID, SnapshotDigest: review.Snapshot.Digest, StateDigest: review.StateDigest, PolicyDigest: review.PolicyDigest, Reason: req.Inputs["reason"]})
		if err != nil {
			return refusal("restore memory", err, req.Target, "Runtime.CommandRestoreMemory")
		}
		if result.SnapshotID != req.Target.ID || result.Generation != req.Target.Revision+1 || result.StateDigest == "" {
			return unproven(req.Target, req.Target.ID, ErrStaleTarget)
		}
		return memorySuccess(req.Target, fmt.Sprintf("Restored %d records as candidates at generation %d; %s", len(result.Restored), result.Generation, result.ProjectionStatus), result.StateDigest, "Runtime.CommandRestoreMemory"), nil
	}}
}
func memoryRestoreTarget(review app.MemoryRestoreReview) Target {
	return Target{Kind: "memory-snapshot", ID: review.Snapshot.ID, Revision: review.Generation, Digest: memoryBindingDigest(review), Scope: "snapshot record set; other records preserved", Summary: fmt.Sprintf("Restore %s #%s (%d records) as candidates; current generation %d", review.Snapshot.Name, shortDigest(review.Snapshot.Digest), len(review.Snapshot.Records), review.Generation)}
}
func validateMemoryBool(v string) error {
	if v != "true" && v != "false" {
		return fmt.Errorf("enter true or false")
	}
	return nil
}
func memorySuccess(target Target, detail, evidence, source string) Outcome {
	return Outcome{Verdict: VerdictPass, Detail: detail, Target: target, Evidence: evidence, Proof: Known(detail, source)}
}

func (a *runtimeControlAuthority) memoryActionContext(ctx context.Context) (context.Context, error) {
	if a == nil || a.runtime == nil || a.localControl == nil {
		return ctx, fmt.Errorf("authenticated local operator required")
	}
	if a.localControlErr != nil {
		return ctx, a.localControlErr
	}
	return a.localControl.Context(ctx), nil
}
func (a *runtimeControlAuthority) ReviewMemoryActionRecord(ctx context.Context, id string) (model.MemoryRecordV2, error) {
	ctx, err := a.memoryActionContext(ctx)
	if err != nil {
		return model.MemoryRecordV2{}, err
	}
	return a.runtime.ReviewMemoryRecord(ctx, id)
}
func (a *runtimeControlAuthority) PromoteMemoryAction(ctx context.Context, e app.CommandEnvelope, req app.PromoteRequest) (model.MemoryRecordV2, error) {
	ctx, err := a.memoryActionContext(ctx)
	if err != nil {
		return model.MemoryRecordV2{}, err
	}
	return a.runtime.CommandPromoteMemory(ctx, e, req)
}
func (a *runtimeControlAuthority) ReviewMemoryActionFile(ctx context.Context, path string) (app.MemoryFileBinding, error) {
	ctx, err := a.memoryActionContext(ctx)
	if err != nil {
		return app.MemoryFileBinding{}, err
	}
	return a.runtime.ReviewMemoryImport(ctx, path)
}
func (a *runtimeControlAuthority) ImportMemoryAction(ctx context.Context, e app.CommandEnvelope, req app.MemoryImportRequest) (app.MemoryImportReadback, error) {
	ctx, err := a.memoryActionContext(ctx)
	if err != nil {
		return app.MemoryImportReadback{}, err
	}
	return a.runtime.CommandImportMemory(ctx, e, req)
}
func (a *runtimeControlAuthority) ReviewMemoryActionExport(ctx context.Context, path, format string) (app.MemoryFileBinding, error) {
	ctx, err := a.memoryActionContext(ctx)
	if err != nil {
		return app.MemoryFileBinding{}, err
	}
	return a.runtime.ReviewMemoryExport(ctx, path, format)
}
func (a *runtimeControlAuthority) ExportMemoryAction(ctx context.Context, e app.CommandEnvelope, req app.MemoryExportRequest) (app.MemoryFileBinding, error) {
	ctx, err := a.memoryActionContext(ctx)
	if err != nil {
		return app.MemoryFileBinding{}, err
	}
	return a.runtime.CommandExportMemory(ctx, e, req)
}
func (a *runtimeControlAuthority) ReviewMemoryActionSlot(ctx context.Context, task string, slot working.SlotType) (app.SlotPromotionReview, error) {
	ctx, err := a.memoryActionContext(ctx)
	if err != nil {
		return app.SlotPromotionReview{}, err
	}
	return a.runtime.ReviewMemorySlot(ctx, task, slot)
}
func (a *runtimeControlAuthority) PromoteMemoryActionSlot(ctx context.Context, e app.CommandEnvelope, review app.SlotPromotionReview, kind model.MemoryKind, title string) (model.MemoryRecordV2, error) {
	ctx, err := a.memoryActionContext(ctx)
	if err != nil {
		return model.MemoryRecordV2{}, err
	}
	return a.runtime.CommandPromoteMemorySlot(ctx, e, review, kind, title)
}
func (a *runtimeControlAuthority) ReviewMemoryActionRestore(ctx context.Context, id string) (app.MemoryRestoreReview, error) {
	ctx, err := a.memoryActionContext(ctx)
	if err != nil {
		return app.MemoryRestoreReview{}, err
	}
	return a.runtime.ReviewMemoryRestore(ctx, id)
}
func (a *runtimeControlAuthority) RestoreMemoryAction(ctx context.Context, e app.CommandEnvelope, req app.MemoryRestoreRequest) (model.MemoryRestoreResult, error) {
	ctx, err := a.memoryActionContext(ctx)
	if err != nil {
		return model.MemoryRestoreResult{}, err
	}
	return a.runtime.CommandRestoreMemory(ctx, e, req)
}
func (a *runtimeControlAuthority) CreateMemorySnapshotAction(ctx context.Context, e app.CommandEnvelope, name string, ids []string) (model.MemoryRestoreSnapshot, error) {
	ctx, err := a.memoryActionContext(ctx)
	if err != nil {
		return model.MemoryRestoreSnapshot{}, err
	}
	return a.runtime.CreateMemorySnapshot(ctx, e, name, ids)
}
func (a *runtimeControlAuthority) ListMemoryRestoreSnapshots(ctx context.Context) ([]model.MemoryRestoreSnapshot, error) {
	ctx, err := a.memoryActionContext(ctx)
	if err != nil {
		return nil, err
	}
	return a.runtime.MemorySnapshots(ctx)
}
