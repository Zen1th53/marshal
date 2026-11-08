//go:build !governed

package tui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"unicode"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

type memoryRestoreAuthority interface {
	RetainedMemorySnapshots(context.Context, string, string, int) ([]store.MemorySnapshot, error)
	CommandPreviewMemorySnapshot(context.Context, app.CommandEnvelope, string, string) (store.MemorySnapshotPreview, error)
	CommandRestoreMemorySnapshot(context.Context, app.CommandEnvelope, store.MemorySnapshotPreview) (store.MemorySnapshotRestore, error)
}

func (a *runtimeControlAuthority) RetainedMemorySnapshots(ctx context.Context, project, session string, limit int) ([]store.MemorySnapshot, error) {
	if a.runtime == nil || a.localControl == nil {
		return nil, errNoRuntime
	}
	return a.runtime.RetainedMemorySnapshots(a.localControl.Context(ctx), project, session, limit)
}
func (a *runtimeControlAuthority) CommandPreviewMemorySnapshot(ctx context.Context, e app.CommandEnvelope, digest, head string) (store.MemorySnapshotPreview, error) {
	if a.runtime == nil || a.localControl == nil {
		return store.MemorySnapshotPreview{}, errNoRuntime
	}
	return a.runtime.CommandPreviewMemorySnapshot(a.localControl.Context(ctx), e, digest, head)
}
func (a *runtimeControlAuthority) CommandRestoreMemorySnapshot(ctx context.Context, e app.CommandEnvelope, p store.MemorySnapshotPreview) (store.MemorySnapshotRestore, error) {
	if a.runtime == nil || a.localControl == nil {
		return store.MemorySnapshotRestore{}, errNoRuntime
	}
	return a.runtime.CommandRestoreMemorySnapshot(a.localControl.Context(ctx), e, p)
}

func memoryRestoreData(value string, limit int) string {
	value = RedactContent(value, nil)
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			return ' '
		}
		return r
	}, value)
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return value
}
func memoryRestoreHex(value string, lengths ...int) error {
	valid := false
	for _, size := range lengths {
		valid = valid || len(value) == size
	}
	if !valid || strings.Trim(value, "0123456789abcdef") != "" {
		return fmt.Errorf("exact lowercase hexadecimal digest is required")
	}
	return nil
}
func memoryRestoreDigest(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func configureMemoryRestoreBinding(s *ControlSource, bindings map[ActionID]Binding) {
	authority, ok := s.Authority.(memoryRestoreAuthority)
	if !ok {
		return
	}
	b := bindings["CTUI-0071"]
	// A durable memory-state snapshot restore (snapshot ID and rationale) is
	// reached through the same action. The selected-record mechanism stays
	// canonical whenever its digest and HEAD binding is typed; without them the
	// snapshot ID is resolved by the memory-state restore.
	stateRestore := b
	b.Gap = ""
	b.Title = "Restore selected existing memory records"
	b.Prepare = nil
	optionalHex := func(lengths ...int) func(string) error {
		return func(value string) error {
			if value == "" {
				return nil
			}
			return memoryRestoreHex(value, lengths...)
		}
	}
	b.Inputs = []InputSpec{{Key: "snapshot_id", Label: "Exact durable memory snapshot ID", Required: true, Validate: canonicalIDValidator("memory snapshot ID")}}
	if stateRestore.PrepareRequest != nil && stateRestore.Execute != nil {
		b.Inputs = append(b.Inputs, InputSpec{Key: "reason", Label: "Restore rationale", Required: true})
	} else {
		stateRestore = Binding{}
	}
	b.Inputs = append(b.Inputs,
		InputSpec{Key: "snapshot_digest", Label: "Exact snapshot digest shown below (64 hex characters; selected-record snapshots only)", Required: stateRestore.Execute == nil, Advanced: stateRestore.Execute != nil, Validate: optionalHexUnless(stateRestore.Execute == nil, optionalHex(64), func(value string) error { return memoryRestoreHex(value, 64) })},
		InputSpec{Key: "head", Label: "Current full repository HEAD (40 or 64 hex characters; selected-record snapshots only)", Required: stateRestore.Execute == nil, Advanced: stateRestore.Execute != nil, Validate: optionalHexUnless(stateRestore.Execute == nil, optionalHex(40, 64), func(value string) error { return memoryRestoreHex(value, 40, 64) })})
	// The typed preview is owned by this one confirmation binding, never by
	// user form text or an inferred branch head. Reopening builds a fresh one.
	var mu sync.Mutex
	var preview *store.MemorySnapshotPreview
	var inputsDigest string
	prepare := func(ctx context.Context, req ActionRequest) (Target, error) {
		mu.Lock()
		defer mu.Unlock()
		if req.ProjectID != s.ProjectID || req.SessionID != s.SessionID {
			return Target{}, ErrStaleTarget
		}
		id, digest, head := strings.TrimSpace(req.Inputs["snapshot_id"]), strings.TrimSpace(req.Inputs["snapshot_digest"]), strings.TrimSpace(req.Inputs["head"])
		if err := validateCanonicalID("memory snapshot ID", id); err != nil {
			return Target{}, err
		}
		if err := memoryRestoreHex(digest, 64); err != nil {
			return Target{}, err
		}
		if err := memoryRestoreHex(head, 40, 64); err != nil {
			return Target{}, err
		}
		snapshots, err := authority.RetainedMemorySnapshots(ctx, s.ProjectID, s.SessionID, 50)
		if err != nil {
			return Target{}, err
		}
		var selected *store.MemorySnapshot
		for i := range snapshots {
			if snapshots[i].ID == id {
				selected = &snapshots[i]
				break
			}
		}
		if selected == nil || selected.Digest != digest || selected.Version != 1 || selected.Binding.ProjectID != s.ProjectID || selected.Binding.SessionID != s.SessionID || selected.RecordCount < 1 || selected.RecordCount > 128 || len(selected.MemoryIDs) != selected.RecordCount || len(selected.Records) != 0 {
			return Target{}, ErrStaleTarget
		}
		key := memoryRestoreDigest(struct{ ID, Digest, Head string }{id, digest, head})
		if preview == nil {
			nonce, err := model.NewID("ui-memory-preview-")
			if err != nil {
				return Target{}, err
			}
			value, err := authority.CommandPreviewMemorySnapshot(ctx, app.CommandEnvelope{ProjectID: s.ProjectID, SessionID: s.SessionID, TargetID: "memory-snapshot:" + id, ExpectedVersion: 1, IdempotencyKey: nonce}, digest, head)
			if err != nil {
				return Target{}, err
			}
			preview = &value
			inputsDigest = key
		}
		if inputsDigest != key || preview.SnapshotID != id || preview.SnapshotDigest != digest || preview.SnapshotVersion != 1 || preview.Binding.ProjectID != s.ProjectID || preview.Binding.SessionID != s.SessionID || preview.Binding.HeadCommit != head || len(preview.Changes) != selected.RecordCount {
			return Target{}, ErrStaleTarget
		}
		var current []model.MemoryRecordV2
		for i, change := range preview.Changes {
			if change.MemoryID != selected.MemoryIDs[i] {
				return Target{}, ErrStaleTarget
			}
			rec, err := s.Authority.MemoryRecord(ctx, change.MemoryID)
			if err != nil {
				return Target{}, err
			}
			current = append(current, rec)
		}
		if memoryRestoreDigest(current) != preview.CurrentDigest {
			return Target{}, fmt.Errorf("%w: selected record state moved after preview; reopen for a fresh preview", ErrStaleTarget)
		}
		summary := fmt.Sprintf("restore %d selected existing records from %s; durable preview %s", len(preview.Changes), memoryRestoreData(id, 64), memoryRestoreData(preview.ID, 64))
		for i, change := range preview.Changes {
			if i >= 2 {
				summary += fmt.Sprintf("; %d further records bound by the exact preview digest", len(preview.Changes)-i)
				break
			}
			summary += fmt.Sprintf("; data %s before [%s] after [%s]", memoryRestoreData(change.MemoryID, 40), memoryRestoreData(change.BeforeBody, 80), memoryRestoreData(change.AfterBody, 80))
		}
		return Target{Kind: "memory-snapshot", ID: id, Revision: 1, Digest: memoryRestoreDigest(*preview), Scope: selectedRestoreScope, Summary: summary}, nil
	}
	b.PrepareRequest = func(ctx context.Context, req ActionRequest) (Target, error) {
		if stateRestore.PrepareRequest != nil && strings.TrimSpace(req.Inputs["snapshot_digest"]) == "" && strings.TrimSpace(req.Inputs["head"]) == "" {
			return stateRestore.PrepareRequest(ctx, req)
		}
		return prepare(ctx, req)
	}
	selectedExecute := func(ctx context.Context, req ActionRequest) (Outcome, error) {
		mu.Lock()
		if preview == nil {
			mu.Unlock()
			return Outcome{}, ErrStaleTarget
		}
		exact := *preview
		mu.Unlock()
		if req.Target.Kind != "memory-snapshot" || req.ProjectID != s.ProjectID || req.SessionID != s.SessionID || req.Target.ID != exact.SnapshotID || req.Target.Revision != exact.SnapshotVersion || req.Target.Digest != memoryRestoreDigest(exact) || req.Inputs["snapshot_id"] != exact.SnapshotID || req.Inputs["snapshot_digest"] != exact.SnapshotDigest || req.Inputs["head"] != exact.Binding.HeadCommit {
			return Outcome{}, ErrStaleTarget
		}
		result, err := authority.CommandRestoreMemorySnapshot(ctx, app.CommandEnvelope{ProjectID: req.ProjectID, SessionID: req.SessionID, TargetID: "memory-snapshot:" + req.Target.ID, ExpectedVersion: 1, IdempotencyKey: req.IdempotencyKey}, exact)
		if err != nil {
			return refusal("selected memory restoration refused", err, req.Target, "internal/app/memory_snapshot_extreme.go")
		}
		if result.PreviewID != exact.ID || result.SnapshotID != exact.SnapshotID || len(result.Revisions) != len(exact.Changes) {
			return unproven(req.Target, result.SnapshotID, ErrStaleTarget)
		}
		for _, change := range exact.Changes {
			version, found := result.Revisions[change.MemoryID]
			if !found || version != change.CurrentRevision+1 {
				return unproven(req.Target, result.SnapshotID, ErrStaleTarget)
			}
			rec, err := s.Authority.MemoryRecord(ctx, change.MemoryID)
			if err != nil {
				return unproven(req.Target, result.SnapshotID, err)
			}
			if rec.ID != change.MemoryID || rec.Revision != version || rec.Lifecycle != model.MemoryCandidate || rec.Confidence != model.ConfidenceUnverified || rec.Source.Kind != "memory_restore" || rec.Source.Reference != result.SnapshotID || rec.SessionID != s.SessionID {
				return unproven(req.Target, result.SnapshotID, ErrStaleTarget)
			}
		}
		detail := fmt.Sprintf("%d selected records restored as candidate/unverified from snapshot %s; current access controls and unselected records retained; independent verification remains required", len(result.Revisions), result.SnapshotID)
		return Outcome{Verdict: VerdictPass, Detail: detail, Target: req.Target, Proof: Known(detail, "internal/store/memory_snapshot_extreme.go")}, nil
	}
	b.Execute = func(ctx context.Context, req ActionRequest) (Outcome, error) {
		if stateRestore.Execute != nil && req.Target.Scope != selectedRestoreScope {
			return stateRestore.Execute(ctx, req)
		}
		return selectedExecute(ctx, req)
	}
	bindings[b.Action] = b
}

func optionalHexUnless(required bool, optional, strict func(string) error) func(string) error {
	if required {
		return strict
	}
	return optional
}

const selectedRestoreScope = "only this snapshot's selected existing records; current access controls retained; restored candidate/unverified; unselected records retained"

func buildRecoverySnapshot(ctx context.Context, s *ControlSource, snap *ControlSnapshot) {
	authority, ok := s.Authority.(memoryRestoreAuthority)
	if !ok {
		return
	}
	snapshots, err := authority.RetainedMemorySnapshots(ctx, s.ProjectID, s.SessionID, 50)
	if err != nil {
		snap.MemoryRestoreSnapshots = []Field{{Label: "Selected-record snapshots", Value: Errored(err.Error(), "internal/app/memory_snapshot_extreme.go")}}
		return
	}
	if len(snapshots) == 0 {
		snap.MemoryRestoreSnapshots = []Field{{Label: "Selected-record snapshots", Value: Empty("internal/app/memory_snapshot_extreme.go")}}
		return
	}
	for i, snapshot := range snapshots {
		if i >= 5 {
			snap.MemoryRestoreSnapshots = append(snap.MemoryRestoreSnapshots, Field{Label: "Additional snapshots", Value: Known(fmt.Sprintf("%d retained; discovery API returns at most 50", len(snapshots)-i), "internal/app/memory_snapshot_extreme.go")})
			break
		}
		snap.MemoryRestoreSnapshots = append(snap.MemoryRestoreSnapshots, Field{Label: memoryRestoreData(snapshot.ID, 80), Value: Known(fmt.Sprintf("version %d; %d selected records; digest %s", snapshot.Version, snapshot.RecordCount, snapshot.Digest), "internal/app/memory_snapshot_extreme.go")})
	}
}
func (v *NavView) buildActionSupplement(action *Node, width int) []string {
	if action.SpecID != "CTUI-0071" || !v.control.snapValid {
		return nil
	}
	return RenderFields(v.control.snap.MemoryRestoreSnapshots, width)
}
