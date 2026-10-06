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
// surface. Evidence must commit before a grant becomes usable. Read grants last
// for this runtime instance; restarting does not resurrect them.
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
	if req.Kind != "read" && req.Kind != "memory" && req.Kind != "network" {
		return model.ErrInvalid
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
			return model.ErrNotFound
		}
	}
	id, err := model.NewID("EVENT-PERMISSION-")
	if err != nil {
		return err
	}
	if err = r.store.AppendEvent(ctx, nil, model.Event{ID: id, Type: "PERMISSION_DECIDED", ProjectID: r.ProjectID(), Timestamp: time.Now().UTC(), Data: map[string]any{"kind": req.Kind, "object": req.Object, "scope": req.Scope, "requester": req.Who, "reason": req.Reason, "allow": allow, "actor": p.ID(), "source": source, "runtime_instance": r.runtimeInstanceID, "run_id": req.RunID, "task_id": req.TaskID}}); err != nil {
		return err
	}
	if req.Kind == "read" {
		if !filepath.IsAbs(req.Object) {
			return model.ErrInvalid
		}
		if r.readGrants == nil {
			r.readGrants = map[string]bool{}
		}
		grant := filepath.Clean(req.Object)
		if resolved, err := filepath.EvalSymlinks(grant); err == nil {
			grant = resolved
		}
		r.readGrants[grant] = allow
	}
	if req.Kind == "memory" && allow {
		if err = security.NewFirewall(security.FirewallConfig{}).ScanRecord(ctx, rec); err != nil {
			return err
		}
		// Operator-approved session imports become project memory; source/session
		// fields retain their original provenance.
		if rec.Scope == string(model.ScopeSession) {
			rec.Scope = string(model.ScopeProject)
			rec.ScopeID = r.ProjectID()
			rec.Lifecycle = model.MemoryDurable
		}
		rec.ContentDigest = rec.CanonicalDigest()
		if err = r.store.WriteMemoryV2(ctx, rec); err != nil {
			return err
		}
		if r.memoryService != nil {
			if err = r.memoryService.IndexRecord(ctx, rec); err != nil {
				return err
			}
		}
		delete(r.continuationCandidates, req.Object)
	}
	return nil
}
func (r *Runtime) HasReadGrant(path string) bool {
	if r == nil {
		return false
	}
	// Resolve the requested object at each read, rejecting symlink escapes.
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	r.permissionMu.Lock()
	defer r.permissionMu.Unlock()
	// The most specific decision wins, including a denial below an allowed root.
	allowed, specificity := false, -1
	for grant, decision := range r.readGrants {
		rel, err := filepath.Rel(grant, resolved)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			continue
		}
		if len(grant) > specificity {
			allowed, specificity = decision, len(grant)
		}
	}
	return allowed
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

// ProposeContinuation keeps candidates in volatile review state. No memory
// write, index update or secret-bearing record occurs before operator approval.
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
	dropped := false
	records := []model.MemoryRecordV2{}
	for _, message := range tr.Messages {
		if (message.Role != "user" && message.Role != "assistant") || strings.TrimSpace(message.Content) == "" {
			continue
		}
		if fw.ScanText(message.Content) != nil {
			dropped = true
			continue
		}
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
		records = append(records, data.ImportedRecords...)
	}
	for _, rec := range records {
		if err := r.proposeMemory(ctx, rec); err != nil {
			return nil, dropped, err
		}
	}

	return records, dropped, nil
}
func (r *Runtime) ContinuationCandidates() []model.MemoryRecordV2 {
	r.permissionMu.Lock()
	defer r.permissionMu.Unlock()
	out := []model.MemoryRecordV2{}
	for _, rec := range r.continuationCandidates {
		out = append(out, rec)
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
				if fw.ScanText(line) != nil {
					dropped = true
					continue
				}
				lines = append(lines, line)
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

func (r *Runtime) proposeMemory(ctx context.Context, rec model.MemoryRecordV2) error {
	if err := security.NewFirewall(security.FirewallConfig{}).ScanRecord(ctx, rec); err != nil {
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
	r.permissionMu.Lock()
	if r.continuationCandidates == nil {
		r.continuationCandidates = map[string]model.MemoryRecordV2{}
	}
	if _, exists := r.continuationCandidates[rec.ID]; exists {
		r.permissionMu.Unlock()
		return nil
	}
	r.continuationCandidates[rec.ID] = rec
	sink := r.permissionSink
	r.permissionMu.Unlock()
	if sink != nil && ctx.Value(memoryReviewKey{}) != r {
		sink(permission.Request{Kind: "memory", Object: rec.ID, Scope: "project memory, persistent", Who: "Marshal", Reason: fmt.Sprintf("Retain candidate from %v, session %s, date %s: %s", rec.ExtMeta["provider"], rec.SessionID, rec.ObservedAt.Format(time.RFC3339), rec.Body)})
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
