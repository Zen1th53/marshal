package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/memory/importer"
	"github.com/Zen1th53/marshal/internal/memory/security"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/store"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

// CommandPermission accepts decisions only from the authenticated local control
// surface. Evidence must commit before a grant becomes usable.
// MarshalToolPermissionKind is a tool call the Marshal model asked to run in
// the Board's native chat (TUI3-0013). Deciding it records only the
// PERMISSION_DECIDED audit; the answer goes back to the provider through the
// chat transport.
const MarshalToolPermissionKind = "marshal-tool"

func (r *Runtime) CommandPermission(ctx context.Context, req permission.Request, allow bool, source string) error {
	p, ok := auth.LocalFromContext(ctx)
	if r == nil || r.store == nil || !ok || ctx.Value(localControlKey{}) != r || p.ProjectID() != r.ProjectIdentity() {
		return authz.ErrDenied
	}
	if _, err := permission.Render([]permission.Request{req}); err != nil {
		return err
	}
	if req.Kind == "read" && !filepath.IsAbs(req.Object) {
		return model.ErrInvalid
	}
	if req.Kind != "read" && req.Kind != "memory" && req.Kind != "network" && req.Kind != "credential" && req.Kind != "credential-bound" && req.Kind != "marshal-command" && req.Kind != MarshalToolPermissionKind && !privateProjectContractKind(req) {
		return model.ErrInvalid
	}
	var boundLease *boundCredentialLease
	if req.Kind == "credential-bound" {
		var checkErr error
		boundLease, checkErr = r.checkBoundCredentialDecision(ctx, req, allow)
		if checkErr != nil {
			return checkErr
		}
		defer func() { r.abortBoundCredentialDecision(boundLease) }()
	}
	if privateProjectContractKind(req) {
		release, err := admitRunControlLifecycle(r)
		if err != nil {
			return err
		}
		defer release()
	}
	if req.Kind == "credential" {
		if !credentialProvider(req.Object) {
			return model.ErrInvalid
		}
		req.Scope = "this project, until revoked"
		req.Who = "MARSHAL"
		req.Reason = "operator credential broker decision"
	}
	if security.NewFirewall(security.FirewallConfig{}).ScanText(req.Reason) != nil {
		req.Reason = "Secrets were dropped."
	}
	r.permissionMu.Lock()
	defer r.permissionMu.Unlock()
	var rec model.MemoryRecordV2
	if req.Kind == "memory" && allow {
		var found bool
		rec, found = r.continuationCandidates[req.Object]
		if !found {
			var err error
			rec, err = r.store.GetMemoryV2(ctx, r.ProjectID(), req.Object)
			if err != nil || rec.Lifecycle != model.MemoryCandidate {
				return model.ErrNotFound
			}
		}
	}
	id, err := model.NewID("EVENT-PERMISSION-")
	if err != nil {
		return err
	}
	audit := model.Event{ID: id, Type: "PERMISSION_DECIDED", ProjectID: r.ProjectID(), Timestamp: time.Now().UTC(), Data: buildPermissionAudit(ctx, map[string]any{"kind": req.Kind, "object": req.Object, "scope": req.Scope, "requester": req.Who, "reason": req.Reason, "allow": allow, "actor": p.ID(), "source": source, "runtime_instance": r.runtimeInstanceID, "run_id": req.RunID, "task_id": req.TaskID, "continuation_provider": req.ContinuationProvider, "approval_binding": req.ApprovalBinding, "source_occurrence": req.SourceOccurrence})}
	if boundLease != nil {
		scope, _ := json.Marshal(boundLease.scope)
		audit.Data["bound_scope"] = string(scope)
		audit.Data["bound_expires"] = boundLease.expires.Format(time.RFC3339Nano)
	}
	if handled, hookErr := r.commitBrokerProjectContractPermission(ctx, req, allow, audit); handled {
		return hookErr
	}
	if handled, hookErr := r.commitBrokerMemoryPermission(ctx, req, allow, rec, audit); handled {
		return hookErr
	}
	if err = r.store.AppendEvent(ctx, nil, audit); err != nil {
		return err
	}
	if boundLease != nil {
		r.finishBoundCredentialDecision(boundLease, allow, id)
	}
	if req.Kind == "credential" && !allow {
		r.egressMu.Lock()
		for _, scope := range r.egressRuns {
			if scope.provider == req.Object && scope.broker != nil {
				if err := scope.proxy.Close(); err != nil {
					r.egressMu.Unlock()
					return err
				}
				if err := r.recordBrokerClosed(ctx, scope, id); err != nil {
					r.egressMu.Unlock()
					return err
				}
			}
		}
		r.egressMu.Unlock()
	}
	if req.Kind == "read" {
		if !filepath.IsAbs(req.Object) {
			return model.ErrInvalid
		}
		grant := filepath.Clean(req.Object)
		if resolved, err := filepath.EvalSymlinks(grant); err == nil {
			grant = resolved
		}
		if err := r.store.SetOperatorReadGrant(ctx, r.ProjectIdentity(), grant, allow); err != nil {
			return err
		}
	}
	if req.Kind == "memory" && allow {
		// Approving memory never stops in Extreme for a secret: it is removed
		// and the record names the redaction. Community refuses.
		if rec, _, err = security.NewFirewall(security.FirewallConfig{}).AdmitRecord(ctx, rec); err != nil {
			return err
		}
		// Operator-approved session imports become project memory; source/session
		// fields retain their original provenance.
		approved, updateErr := r.store.UpdateMemory(ctx, rec.ProjectID, rec.ID, rec.Revision, func(existing *model.MemoryRecordV2) error {
			if existing.Lifecycle != model.MemoryCandidate {
				return model.ErrConflict
			}
			existing.Lifecycle = model.MemoryDurable
			if existing.Scope == string(model.ScopeSession) {
				existing.Scope = string(model.ScopeProject)
				existing.ScopeID = r.ProjectID()
			}
			return nil
		})
		if updateErr != nil {
			err = updateErr
			return err
		}
		if r.memoryService != nil {
			if err = r.memoryService.IndexRecord(ctx, approved); err != nil {
				return err
			}
		}
		delete(r.continuationCandidates, req.Object)
	}
	if req.Kind == "memory" && !allow {
		rejected, getErr := r.store.GetMemoryV2(ctx, r.ProjectID(), req.Object)
		if getErr != nil {
			return getErr
		}
		if rejected.Lifecycle != model.MemoryCandidate {
			return model.ErrConflict
		}
		rejected, err = r.store.UpdateMemory(ctx, rejected.ProjectID, rejected.ID, rejected.Revision, func(existing *model.MemoryRecordV2) error {
			existing.Lifecycle = model.MemoryRejected
			if existing.ExtMeta == nil {
				existing.ExtMeta = map[string]any{}
			}
			existing.ExtMeta["rejected_by"] = p.ID()
			return nil
		})
		if err != nil {
			return err
		}
		if r.memoryService != nil {
			if err := r.memoryService.IndexRecord(ctx, rejected); err != nil {
				return err
			}
		}
		delete(r.continuationCandidates, req.Object)
	}
	return nil
}
func (r *Runtime) HasReadGrant(path string) bool {
	if r == nil || r.store == nil {
		return false
	}
	// Resolve the requested object at each read, rejecting symlink escapes.
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	grants, err := r.store.ListOperatorReadGrants(context.Background(), r.ProjectIdentity())
	if err != nil {
		return false
	}
	// The most specific decision wins, including a denial below an allowed root.
	allowed, specificity := false, -1
	for _, grant := range grants {
		rel, err := filepath.Rel(grant.Folder, resolved)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			continue
		}
		if len(grant.Folder) > specificity {
			allowed, specificity = grant.Allowed, len(grant.Folder)
		}
	}
	if specificity < 0 {
		// No recorded decision covers the path. Community denies; the private
		// edition reads it and records the automatic decision once.
		return r.buildUndecidedRead(resolved)
	}
	return allowed
}

// CommandRevokeReadGrant removes one exact operator decision. It is available
// only through the authenticated local control surface.
func (r *Runtime) CommandRevokeReadGrant(ctx context.Context, folder string) error {
	p, ok := auth.LocalFromContext(ctx)
	if r == nil || r.store == nil || !ok || ctx.Value(localControlKey{}) != r || p.ProjectID() != r.ProjectIdentity() {
		return authz.ErrDenied
	}
	if !filepath.IsAbs(folder) {
		return model.ErrInvalid
	}
	canonical := filepath.Clean(folder)
	if resolved, err := filepath.EvalSymlinks(canonical); err == nil {
		canonical = resolved
	}
	id, err := model.NewID("EVENT-PERMISSION-")
	if err != nil {
		return err
	}
	if err := r.store.AppendEvent(ctx, nil, model.Event{ID: id, Type: "PERMISSION_DECIDED", ProjectID: r.ProjectID(), Timestamp: time.Now().UTC(), Data: map[string]any{"kind": "read", "object": canonical, "allow": false, "action": "revoke", "actor": p.ID(), "source": "operator revoke", "runtime_instance": r.runtimeInstanceID}}); err != nil {
		return err
	}
	return r.store.RevokeOperatorReadGrant(ctx, r.ProjectIdentity(), canonical)
}

func (r *Runtime) OperatorReadGrants(ctx context.Context) ([]store.OperatorReadGrant, error) {
	if r == nil || r.store == nil {
		return nil, model.ErrUnavailable
	}
	return r.store.ListOperatorReadGrants(ctx, r.ProjectIdentity())
}
func (r *Runtime) ReadGranted(ctx context.Context, path string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !r.HasReadGrant(path) {
		return nil, authz.ErrDenied
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	// On Linux check the opened object, not just a path that can be swapped.
	openedPath := path
	if runtime.GOOS == "linux" {
		openedPath = fmt.Sprintf("/proc/self/fd/%d", f.Fd())
	}
	if !r.HasReadGrant(openedPath) {
		return nil, authz.ErrDenied
	}
	data, err := io.ReadAll(io.LimitReader(f, 32<<20+1))
	if len(data) > 32<<20 {
		return nil, fmt.Errorf("history exceeds 32 MiB")
	}
	return data, err
}

// ProposeContinuation persists sanitized candidates for operator review. They
// remain inactive until an operator approves them.
func (r *Runtime) ProposeContinuation(ctx context.Context, tr importer.SessionTranscript) ([]model.MemoryRecordV2, bool, error) {
	if canonicalSessionRoot(tr.CWD) != canonicalSessionRoot(r.ProjectRoot()) {
		return nil, false, fmt.Errorf("history belongs to another project")
	}
	fw := security.NewFirewall(security.FirewallConfig{})
	for _, metadata := range []string{tr.SessionID, tr.Provider, tr.CWD, tr.Branch, tr.TaskID} {
		if fw.ScanText(metadata) != nil {
			return nil, true, nil
		}
	}
	if isMarshalRoleTurn(tr) {
		declined, err := r.declineMarshalRoleTurn(ctx, tr)
		if err != nil || declined {
			return []model.MemoryRecordV2{}, false, err
		}
	}
	dropped := false
	var first model.MemoryRecordV2
	var entries []string
	for _, message := range tr.Messages {
		if (message.Role != "user" && message.Role != "assistant") || strings.TrimSpace(message.Content) == "" {
			continue
		}
		// Community drops a message holding a secret; Extreme keeps it with
		// the secret removed. Either way the caller learns secrets were met.
		content, redacted, scanErr := fw.AdmitText(message.Content)
		if scanErr != nil {
			dropped = true
			continue
		}
		dropped = dropped || redacted.Applied()
		message.Content = content
		one := tr
		one.Messages = []importer.Message{message}
		raw, err := json.Marshal(one)
		if err != nil {
			return nil, dropped, err
		}
		data, err := importer.NewSessionImporter(importer.Config{}).ImportRawJSON(ctx, r.ProjectID(), raw, true)
		if err != nil {
			return nil, dropped, err
		}
		for _, rec := range data.ImportedRecords {
			if first.ID == "" {
				first = rec
			}
			if rec.ObservedAt.After(first.ObservedAt) {
				first.ObservedAt = rec.ObservedAt
			}
			entries = append(entries, rec.Body)
		}
	}
	if len(entries) == 0 {
		return []model.MemoryRecordV2{}, dropped, nil
	}
	rec, ok, err := r.mergeContinuationEpisode(ctx, tr, first, entries)
	if err != nil || !ok {
		return []model.MemoryRecordV2{}, dropped, err
	}
	return []model.MemoryRecordV2{rec}, dropped, nil
}

// isMarshalRoleTurn reports whether a captured provider session is one of the
// runtime's own noninteractive Marshal role turns: a user prompt opened by
// the role-turn marker (or Antigravity's role-turn preamble). Such sessions
// repeat runtime prompts and bound context; they are not session memory.
func isMarshalRoleTurn(tr importer.SessionTranscript) bool {
	marker := strings.TrimSpace(marshalRoleTurnMarker)
	for _, message := range tr.Messages {
		if message.Role != "user" || message.Kind != importer.MessageKindText {
			continue
		}
		content := strings.TrimSpace(message.Content)
		// Antigravity records plan-mode prompts behind its /plan command.
		content = strings.TrimSpace(strings.TrimPrefix(content, "/plan "))
		if strings.HasPrefix(content, marker) || strings.HasPrefix(content, marshalRoleTurnAgyPreamble) {
			return true
		}
	}
	return false
}

// declineMarshalRoleTurn records a role-turn session's first episode part as
// rejected by the runtime, without a review request, so later captures of the
// same session stop at the rejected part. A session that already holds a
// first part (an interactive session, or a capture made before this rule) is
// left to the ordinary episode merge; declined is then false.
func (r *Runtime) declineMarshalRoleTurn(ctx context.Context, tr importer.SessionTranscript) (bool, error) {
	project := r.ProjectID()
	id := importer.SessionEpisodeID(project, tr.Provider, tr.SessionID, 0)
	if _, err := r.store.GetMemoryV2(ctx, project, id); err == nil {
		return false, nil
	} else if !errors.Is(err, model.ErrNotFound) {
		return false, err
	}
	at := tr.Timestamp.UTC()
	for _, message := range tr.Messages {
		if message.Timestamp.After(at) {
			at = message.Timestamp.UTC()
		}
	}
	if at.IsZero() {
		at = time.Unix(0, 0).UTC()
	}
	rec := model.MemoryRecordV2{
		ID:         id,
		ProjectID:  project,
		Kind:       model.MemoryKindEpisodic,
		Lifecycle:  model.MemoryRejected,
		Confidence: model.ConfidenceObserved,
		Authority:  model.AuthorityAgent,
		Title:      importer.SessionEpisodeTitle(tr.SessionID, tr.Provider, 0),
		Body:       "Noninteractive Marshal role turn; declined by the runtime without review. The turn stays in the provider's own history.",
		Scope:      string(model.ScopeSession),
		ScopeID:    tr.SessionID,
		ObservedAt: at,
		IngestedAt: at,
		ValidFrom:  at,
		CreatedAt:  at,
		UpdatedAt:  at,
		SessionID:  tr.SessionID,
		Source:     model.MemorySource{Kind: "external", Reference: tr.SessionID},
		ExtMeta: map[string]any{
			"provider":             tr.Provider,
			"source_format":        "marshal-json",
			"imported_retroactive": true,
			"session_episode":      true,
			"episode_part":         0,
			"marshal_role_turn":    true,
			"rejected_by":          marshalRuntimeRoleTurnDecider,
		},
	}
	rec.ContentDigest = rec.CanonicalDigest()
	if err := r.store.WriteMemoryV2(ctx, rec); err != nil {
		// A concurrent capture of the same session wrote the part first.
		if _, getErr := r.store.GetMemoryV2(ctx, project, id); getErr == nil {
			return true, nil
		}
		return false, err
	}
	return true, nil
}

// marshalRuntimeRoleTurnDecider names the runtime rule that declines role-turn
// captures, distinct from any operator or Marshal decision.
const marshalRuntimeRoleTurnDecider = "marshal-runtime:role-turn"

// mergeContinuationEpisode folds newly captured messages into the session's
// one open episode candidate. Messages already recorded in any part of this
// session are dropped, the body stays bounded, and only a newly created part
// asks for a review decision. A part that was already decided (retained,
// rejected or expired) is never edited; later messages start the next part.
func (r *Runtime) mergeContinuationEpisode(ctx context.Context, tr importer.SessionTranscript, template model.MemoryRecordV2, entries []string) (model.MemoryRecordV2, bool, error) {
	for attempt := 0; ; attempt++ {
		rec, ok, err := r.mergeContinuationEpisodeOnce(ctx, tr, template, entries)
		// A concurrent capture or decision moved the record; re-read it.
		if errors.Is(err, model.ErrConflict) && attempt < 2 {
			continue
		}
		return rec, ok, err
	}
}

func (r *Runtime) mergeContinuationEpisodeOnce(ctx context.Context, tr importer.SessionTranscript, template model.MemoryRecordV2, entries []string) (model.MemoryRecordV2, bool, error) {
	project := r.ProjectID()
	seen := map[string]bool{}
	for part := 0; part < importer.EpisodeMaxParts; part++ {
		id := importer.SessionEpisodeID(project, tr.Provider, tr.SessionID, part)
		existing, err := r.store.GetMemoryV2(ctx, project, id)
		if err != nil && !errors.Is(err, model.ErrNotFound) {
			return model.MemoryRecordV2{}, false, err
		}
		found := err == nil
		if found {
			for _, d := range episodeDigests(existing) {
				seen[d] = true
			}
		}
		fresh, digests := freshEpisodeEntries(entries, seen)
		if found && existing.Lifecycle == model.MemoryRejected {
			// The session's capture was declined. Opening the next part would
			// ask again on every later turn; when the reviewer is the Marshal
			// whose own chat is being captured, each rejection was itself the
			// next turn, a loop of one review request per message. Later turns
			// stay in the provider's own history.
			return model.MemoryRecordV2{}, false, nil
		}
		if found && existing.Lifecycle != model.MemoryCandidate {
			if len(fresh) == 0 {
				return existing, true, nil
			}
			continue
		}
		if len(fresh) == 0 {
			if found {
				return existing, true, nil
			}
			return model.MemoryRecordV2{}, false, nil
		}
		addition := strings.Join(fresh, "\n")
		if !found {
			rec := template
			rec.ID = id
			rec.Title = importer.SessionEpisodeTitle(tr.SessionID, tr.Provider, part)
			rec.Body = importer.BoundEpisodeBody(addition)
			rec.ExtMeta = cloneExtMeta(template.ExtMeta)
			rec.ExtMeta["session_episode"] = true
			rec.ExtMeta["episode_part"] = part
			rec.ExtMeta["episode_messages"] = len(fresh)
			rec.ExtMeta["episode_message_digests"] = boundedDigests(nil, digests)
			rec.ContentDigest = rec.CanonicalDigest()
			if err := r.proposeMemory(ctx, rec); err != nil {
				return model.MemoryRecordV2{}, false, err
			}
			return rec, true, nil
		}
		updated, err := r.store.UpdateMemory(ctx, project, id, existing.Revision, func(m *model.MemoryRecordV2) error {
			if m.Lifecycle != model.MemoryCandidate {
				return model.ErrConflict
			}
			m.Body = importer.BoundEpisodeBody(m.Body + "\n" + addition)
			if template.ObservedAt.After(m.ObservedAt) {
				m.ObservedAt = template.ObservedAt
			}
			m.ExtMeta = cloneExtMeta(m.ExtMeta)
			m.ExtMeta["episode_messages"] = episodeCount(m.ExtMeta["episode_messages"]) + len(fresh)
			m.ExtMeta["episode_message_digests"] = boundedDigests(episodeDigests(*m), digests)
			return nil
		})
		if err != nil {
			return model.MemoryRecordV2{}, false, err
		}
		if r.memoryService != nil {
			if err := r.memoryService.IndexRecord(ctx, updated); err != nil {
				return model.MemoryRecordV2{}, false, err
			}
		}
		// A pending review approves the record by its current revision.
		r.permissionMu.Lock()
		if _, pending := r.continuationCandidates[id]; pending {
			r.continuationCandidates[id] = updated
		}
		r.permissionMu.Unlock()
		return updated, true, nil
	}
	// Every part is used: further turns stay in the provider's own history.
	return model.MemoryRecordV2{}, false, nil
}

// freshEpisodeEntries returns the entries not yet recorded in seen (nor
// repeated within entries). seen itself is not modified.
func freshEpisodeEntries(entries []string, seen map[string]bool) ([]string, []string) {
	var fresh, digests []string
	local := map[string]bool{}
	for _, e := range entries {
		d := importer.EpisodeEntryDigest(e)
		if seen[d] || local[d] {
			continue
		}
		local[d] = true
		fresh = append(fresh, e)
		digests = append(digests, d)
	}
	return fresh, digests
}

func episodeDigests(rec model.MemoryRecordV2) []string {
	var out []string
	switch v := rec.ExtMeta["episode_message_digests"].(type) {
	case []string:
		out = append(out, v...)
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

func boundedDigests(have, add []string) []string {
	out := append(append([]string(nil), have...), add...)
	if len(out) > importer.EpisodeMaxDigests {
		out = out[len(out)-importer.EpisodeMaxDigests:]
	}
	return out
}

func episodeCount(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	case int64:
		return int(n)
	}
	return 0
}

func cloneExtMeta(in map[string]any) map[string]any {
	out := make(map[string]any, len(in)+4)
	for k, v := range in {
		out[k] = v
	}
	return out
}
func (r *Runtime) ContinuationCandidates() []model.MemoryRecordV2 {
	if r == nil || r.store == nil {
		return nil
	}
	out, err := r.store.ListMemoryV2(context.Background(), store.MemoryQueryFilter{
		ProjectID: r.ProjectID(), Lifecycle: model.MemoryCandidate,
	})
	if err != nil {
		return nil
	}
	seen := make(map[string]bool, len(out))
	for _, rec := range out {
		seen[rec.ID] = true
	}
	// Proposals that hold no canonical row yet (live MCP retention requests
	// awaiting the operator) are still reviewable candidates.
	for _, rec := range r.unpersistedContinuationCandidates() {
		if !seen[rec.ID] {
			out = append(out, rec)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ReadContinuation only traverses a recorded folder and only decodes visible
// conversation fields. Project CWD is mandatory; foreign sessions are omitted.
func (r *Runtime) ReadContinuation(ctx context.Context, provider, folder string) ([]model.MemoryRecordV2, bool, error) {
	if !r.HasReadGrant(folder) {
		return nil, false, authz.ErrDenied
	}
	records := []model.MemoryRecordV2{}
	dropped := false
	var adapter importer.HistoryAdapter
	switch provider {
	case "claude":
		adapter = importer.ClaudeJSONLAdapter{}
	case "codex":
		adapter = importer.CodexJSONLAdapter{}
	default:
		return nil, false, model.ErrInvalid
	}
	err := filepath.WalkDir(folder, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if entry.IsDir() {
			if !r.HasReadGrant(path) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".md") && provider == "claude" && canonicalSessionRoot(folder) == canonicalSessionRoot(nativeInventoryFolder(provider, r.ProjectRoot())) {
			rel, _ := filepath.Rel(folder, path)
			if !strings.HasPrefix(rel, "memory"+string(os.PathSeparator)) {
				return nil
			}
			raw, e := r.ReadGranted(ctx, path)
			if e != nil {
				return e
			}
			lines := []string{}
			fw := security.NewFirewall(security.FirewallConfig{})
			for _, line := range strings.Split(string(raw), "\n") {
				clean, redacted, scanErr := fw.AdmitText(line)
				if scanErr != nil {
					dropped = true
					continue
				}
				dropped = dropped || redacted.Applied()
				lines = append(lines, clean)
			}
			info, e := entry.Info()
			if e != nil {
				return e
			}
			if strings.TrimSpace(strings.Join(lines, "\n")) == "" {
				return nil
			}
			tr := importer.SessionTranscript{SessionID: "provider-memory:" + rel, Provider: provider, CWD: r.ProjectRoot(), Timestamp: info.ModTime(), Messages: []importer.Message{{Role: "assistant", Content: strings.Join(lines, "\n")}}}
			proposed, secret, e := r.ProposeContinuation(ctx, tr)
			dropped = dropped || secret
			records = append(records, proposed...)
			return e
		}
		if !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		raw, err := r.ReadGranted(ctx, path)
		if err != nil {
			return err
		}
		tr, err := adapter.Decode(raw)
		if err != nil {
			return fmt.Errorf("decode provider history: %w", err)
		}
		if canonicalSessionRoot(tr.CWD) != canonicalSessionRoot(r.ProjectRoot()) {
			return nil
		}
		tr.Provider = provider
		proposed, secret, err := r.ProposeContinuation(ctx, tr)
		dropped = dropped || secret
		records = append(records, proposed...)
		return err
	})
	return records, dropped, err
}

func nativeInventoryFolder(provider, root string) string {
	env, base, dir := "CODEX_HOME", ".codex", "sessions"
	if provider == "claude" {
		env, base, dir = "CLAUDE_CONFIG_DIR", ".claude", "projects"
	}
	if provider == "opencode" {
		if p := os.Getenv("MARSHAL_OPENCODE_DB"); p != "" {
			return filepath.Dir(p)
		}
		base := os.Getenv("XDG_DATA_HOME")
		if base == "" {
			h, _ := os.UserHomeDir()
			base = filepath.Join(h, ".local", "share")
		}
		return filepath.Join(base, "opencode")
	}
	if provider == "antigravity" {
		h, _ := os.UserHomeDir()
		return filepath.Join(h, ".gemini", "antigravity-cli")
	}
	if provider != "claude" && provider != "codex" {
		return ""
	}
	home := os.Getenv(env)
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		home = filepath.Join(h, base)
	}
	if !filepath.IsAbs(home) {
		home = filepath.Join(root, home)
	}
	if provider == "claude" {
		return filepath.Join(home, dir, regexp.MustCompile(`[^a-zA-Z0-9]`).ReplaceAllString(canonicalSessionRoot(root), "-"))
	}
	return filepath.Join(home, dir)
}

// EarlierWorkFolder returns the provider history folder used for this project.
func (r *Runtime) EarlierWorkFolder(provider string) string {
	return nativeInventoryFolder(provider, r.ProjectRoot())
}

func (r *Runtime) proposeMemory(ctx context.Context, rec model.MemoryRecordV2) error {
	rec, _, err := security.NewFirewall(security.FirewallConfig{}).AdmitRecord(ctx, rec)
	if err != nil {
		return err
	}
	if rec.ProjectID != r.ProjectID() {
		return authz.ErrDenied
	}
	if _, err := r.store.GetMemoryV2(ctx, rec.ProjectID, rec.ID); err == nil {
		return nil
	} else if !errors.Is(err, model.ErrNotFound) {
		return err
	}
	// WriteMemoryV2 applies the same secret firewall used by all canonical
	// memory writes. Candidates stay excluded from retrieval until promoted.
	if err := r.store.WriteMemoryV2(ctx, rec); err != nil {
		return err
	}
	// Index the candidate so a later promotion — including one performed by a
	// separate process (e.g. `memory promote`) — remains discoverable through
	// this runtime's lexical projection. Recall still excludes candidates until
	// they are promoted; the durable record is reloaded from the store.
	if r.memoryService != nil {
		if err := r.memoryService.IndexRecord(ctx, rec); err != nil {
			return err
		}
	}
	r.permissionMu.Lock()
	if r.continuationCandidates == nil {
		r.continuationCandidates = map[string]model.MemoryRecordV2{}
	}
	r.continuationCandidates[rec.ID] = rec
	sink := r.permissionSink
	r.permissionMu.Unlock()
	if sink != nil && ctx.Value(memoryReviewKey{}) != r {
		sink(permission.Request{Kind: "memory", Object: rec.ID, Scope: "project memory, persistent", Who: "Local request (unverified)", Reason: fmt.Sprintf("Retain candidate from %v, session %s, date %s: %s", rec.ExtMeta["provider"], rec.SessionID, rec.ObservedAt.Format(time.RFC3339), rec.Body)})
	}
	return nil
}
func (r *Runtime) SetPermissionSink(sink func(permission.Request)) {
	r.permissionMu.Lock()
	defer r.permissionMu.Unlock()
	r.permissionSink = sink
}

// CommandRemember is the authenticated operator's confirmed memory-review
// form. Its confirmation is an equivalent permission action, recorded before
// persistence; it does not request a second popup for the same entry.
type memoryReviewKey struct{}

func (r *Runtime) CommandRemember(ctx context.Context, req RememberRequest) (model.MemoryRecordV2, error) {
	p, ok := auth.LocalFromContext(ctx)
	if r == nil || r.store == nil || r.memoryService == nil || !ok || ctx.Value(localControlKey{}) != r || p.ProjectID() != r.ProjectIdentity() {
		return model.MemoryRecordV2{}, authz.ErrDenied
	}
	principal := authz.Principal{ID: p.ID(), Role: authz.Role{Name: "orchestrator", Authorities: []authz.Authority{authz.AuthorityTaskPlan}}}
	rec, err := r.memoryService.Remember(context.WithValue(ctx, memoryReviewKey{}, r), principal, req)
	if err != nil {
		return model.MemoryRecordV2{}, err
	}
	decision := permission.Request{Kind: "memory", Object: rec.ID, Scope: "project memory, persistent", Who: "operator", Reason: "Retain reviewed memory: " + rec.Title + ": " + rec.Body}
	if err := r.CommandPermission(ctx, decision, true, "operator memory review"); err != nil {
		return model.MemoryRecordV2{}, err
	}
	return r.store.GetMemoryV2(ctx, rec.ProjectID, rec.ID)
}

// unpersistedContinuationCandidates returns the in-process candidates that have
// no canonical memory row, sorted by ID.
func (r *Runtime) unpersistedContinuationCandidates() []model.MemoryRecordV2 {
	r.permissionMu.Lock()
	pending := make([]model.MemoryRecordV2, 0, len(r.continuationCandidates))
	for _, rec := range r.continuationCandidates {
		pending = append(pending, rec)
	}
	r.permissionMu.Unlock()
	out := pending[:0]
	for _, rec := range pending {
		if _, err := r.store.GetMemoryV2(context.Background(), rec.ProjectID, rec.ID); err == nil {
			continue
		}
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
