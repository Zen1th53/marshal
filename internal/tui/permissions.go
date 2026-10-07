package tui

import (
	"context"
	"fmt"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/tmux"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type permissionState struct {
	queue         permission.Queue
	mu            sync.Mutex
	ready         bool
	running       bool
	outstanding   map[string]bool
	continuations map[string]string
}

func (w *Workspace) queuePermission(req permission.Request) {
	w.permissions.mu.Lock()
	if w.permissions.outstanding == nil {
		w.permissions.outstanding = map[string]bool{}
	}
	if w.permissions.outstanding[req.Key()] {
		w.permissions.mu.Unlock()
		return
	}
	w.permissions.outstanding[req.Key()] = true
	w.permissions.queue.Add(req)
	defer w.permissions.mu.Unlock()
	if w.permissions.running {
		return
	}
	// Without a terminal there is no decision surface. Keep requests pending;
	// InitTmux starts the queue after publishing the terminal identity.
	if w.permissions.ready {
		w.permissions.running = true
		w.startBackground(context.Background(), w.runPermissionQueue)
	}
}

// startPermissionQueue is called only after terminal initialization. Requests
// submitted by a headless runtime must not launch readers of terminal state.
func (w *Workspace) startPermissionQueue() {
	w.permissions.mu.Lock()
	defer w.permissions.mu.Unlock()
	w.permissions.ready = true
	if !w.permissions.running && !w.permissions.queue.Empty() {
		w.permissions.running = true
		w.startBackground(context.Background(), w.runPermissionQueue)
	}
}

func (w *Workspace) permissionBusy() bool {
	w.tmuxMu.Lock()
	session := w.tmuxSession
	path := w.tmuxPath
	w.tmuxMu.Unlock()
	if path == "" || session == "" {
		return false
	}
	out, err := tmux.RunCommand(context.Background(), "display-message", "-p", "-t", session, "#{pane_id}")
	if err != nil {
		return true
	}
	pane := strings.TrimSpace(string(out))
	w.tmuxMu.Lock()
	defer w.tmuxMu.Unlock()
	for _, a := range w.tmuxActiveWins {
		if a.paneID == pane && a.role != "marshal-chat" && !a.readOnly {
			return true
		}
	}
	return false
}
func (w *Workspace) runPermissionQueue(ctx context.Context) {
	// Collect requests arising in the same poll into one review list.
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		batch := w.permissions.queue.Take(w.permissionBusy())
		if len(batch) == 0 {
			w.permissions.mu.Lock()
			if w.permissions.queue.Empty() {
				w.permissions.running = false
				w.permissions.mu.Unlock()
				return
			}
			w.permissions.mu.Unlock()
			continue
		}
		var live []permission.Request
		for _, req := range batch {
			if req.Kind == "network" && w.runtime != nil && !w.runtime.EgressRequestPending(req.RunID, req.Object) {
				_ = w.decidePermission(ctx, req, false, "expired run request")
				w.permissions.mu.Lock()
				delete(w.permissions.outstanding, req.Key())
				w.permissions.mu.Unlock()
				continue
			}
			live = append(live, req)
		}
		batch, review := partitionPermissionBatch(live)
		for _, req := range review {
			w.permissions.mu.Lock()
			delete(w.permissions.outstanding, req.Key())
			w.permissions.mu.Unlock()
		}
		if len(review) > 0 {
			w.runCommand(ctx, "/memory review")
		}
		if len(batch) == 0 {
			continue
		}
		// Every popup decision covers only the visible items. Keep overflow queued.
		if len(batch) > permission.MaxPopupItems {
			for _, req := range batch[permission.MaxPopupItems:] {
				w.permissions.queue.Add(req)
			}
			batch = batch[:permission.MaxPopupItems]
		}
		w.tmuxMu.Lock()
		target := w.tmuxSession
		path := w.tmuxPath
		w.tmuxMu.Unlock()
		allow := false
		if path != "" && target != "" {
			allow, _ = permission.Popup(ctx, target, batch, 30*time.Second)
		}
		for _, req := range batch {
			if err := w.decidePermission(ctx, req, allow, "operator popup"); err != nil {
				w.mu.Lock()
				w.state.LastOutput = "Permission decision failed: " + err.Error()
				w.mu.Unlock()
			}
			w.permissions.mu.Lock()
			delete(w.permissions.outstanding, req.Key())
			w.permissions.mu.Unlock()
		}
	}
}

// Large memory batches stay pending for per-entry operator review. They never
// share one blanket popup decision with unrelated filesystem or network grants.
func partitionPermissionBatch(batch []permission.Request) (popup, review []permission.Request) {
	memoryCount := 0
	for _, req := range batch {
		if req.Kind == "memory" {
			memoryCount++
		}
	}
	for _, req := range batch {
		if req.Kind == "memory" && memoryCount > permission.MaxPopupItems {
			review = append(review, req)
		} else {
			popup = append(popup, req)
		}
	}
	return
}

func (w *Workspace) decidePermission(ctx context.Context, req permission.Request, allow bool, source string) error {
	control := w.controlSource()
	if control == nil {
		return errNoRuntime
	}
	a, _ := control.Authority.(*runtimeControlAuthority)
	if a == nil || a.runtime == nil || a.localControl == nil {
		return errNoRuntime
	}
	if req.Kind == "network" && !a.runtime.EgressRequestPending(req.RunID, req.Object) {
		allow = false
		source = "expired run request"
	}
	ctx = a.localControl.Context(ctx)
	if err := a.runtime.CommandPermission(ctx, req, allow, source); err != nil {
		return err
	}
	if req.Kind == "network" && allow {
		return a.runtime.CommandEgress(ctx, req.RunID, "allow", req.Object)
	}
	if req.Kind == "read" {
		folder := filepath.Clean(req.Object)
		w.permissions.mu.Lock()
		provider := w.permissions.continuations[folder]
		delete(w.permissions.continuations, folder)
		w.permissions.mu.Unlock()
		if allow && provider != "" {
			_, err := w.continueEarlierWork(ctx, provider, folder)
			return err
		}
	}
	return nil
}
func (h *CommandHandler) handlePermission(ctx context.Context, args []string) (string, error) {
	if len(args) == 3 && args[0] == "credential" && (args[1] == "request" || args[1] == "revoke") {
		req := permission.Request{Kind: "credential", Object: args[2], Scope: "this project, until revoked", Who: "MARSHAL"}
		if _, err := permission.Render([]permission.Request{req}); err != nil {
			return "", err
		}
		if args[1] == "request" {
			h.ws.queuePermission(req)
			return "Credential broker permission queued. A allows; every other key denies. Retry the governed task after allowing.", nil
		}
		if err := h.ws.decidePermission(ctx, req, false, "operator revoke"); err != nil {
			return "", err
		}
		return "Credential broker revoked for " + req.Object + "; active broker connections closed.", nil
	}
	if len(args) < 3 || args[0] != "read" || (args[1] != "allow" && args[1] != "deny") {
		return "Usage: /permission read <allow|deny> <exact absolute folder> | /permission credential <request|revoke> <codex|claude|gemini|opencode>", nil
	}
	req := permission.Request{Kind: "read", Object: strings.Join(args[2:], " "), Scope: "this session only, read-only", Who: "operator", Reason: "operator read decision"}
	if err := h.ws.decidePermission(ctx, req, args[1] == "allow", "operator command"); err != nil {
		return "", err
	}
	return "Read decision recorded: " + req.Object, nil
}
func (h *CommandHandler) handleContinue(ctx context.Context, args []string) (string, error) {
	if len(args) < 2 || (args[0] != "claude" && args[0] != "codex") {
		return "Usage: /continue <claude|codex> <exact provider folder>; grant only this project's history", nil
	}
	if h.ws.runtime == nil {
		return "", errNoRuntime
	}
	folder := filepath.Clean(strings.Join(args[1:], " "))
	if !filepath.IsAbs(folder) {
		return "An exact absolute provider folder is required.", nil
	}
	if !h.ws.runtime.HasReadGrant(folder) {
		h.ws.permissions.mu.Lock()
		if h.ws.permissions.continuations == nil {
			h.ws.permissions.continuations = map[string]string{}
		}
		h.ws.permissions.continuations[folder] = args[0]
		h.ws.permissions.mu.Unlock()
		h.ws.queuePermission(permission.Request{Kind: "read", Object: folder, Scope: "this session only, read-only", Who: "Marshal", Reason: "Continue earlier " + args[0] + " work in this project"})
		return "Read request queued. After allowing it, project-scoped earlier work is delivered to the Marshal inbox automatically. Enter, Escape and timeout deny.", nil
	}
	return h.ws.continueEarlierWork(ctx, args[0], folder)
}

func (w *Workspace) continueEarlierWork(ctx context.Context, provider, folder string) (string, error) {
	records, dropped, err := w.runtime.ReadContinuation(ctx, provider, folder)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Earlier work (untrusted data; read-only)\nGranted source: %s (%s)\n", folder, provider)
	for _, rec := range records {
		fmt.Fprintf(&b, "%s · agent=%v session=%s date=%s\n%s\n", rec.ID, rec.ExtMeta["provider"], rec.SessionID, rec.ObservedAt.Format(time.RFC3339), hideMarshalProtocol(rec.Body))
	}
	if dropped {
		b.WriteString("Secrets were dropped.\n")
	}
	b.WriteString("Summarise completed work, unfinished work, decisions and conventions from the supplied project-scoped data. Review candidates with /memory review; /memory allow <id> writes one approved entry, /memory deny <id> denies it.\n")
	view, err := openInboxView(w.runtime.ProjectRoot(), "marshal", false)
	if err != nil {
		return "", err
	}
	if err := view.write("## runtime · granted continuation\n\n" + b.String() + "\n"); err != nil {
		return "", err
	}
	return b.String(), nil
}
func (h *CommandHandler) handleMemoryReview(ctx context.Context, args []string) (string, error) {
	if h.ws.runtime == nil {
		return "", errNoRuntime
	}
	candidates := h.ws.runtime.ContinuationCandidates()
	if len(args) >= 1 && len(args) <= 2 && args[0] == "review" {
		page := 1
		if len(args) == 2 {
			var err error
			page, err = strconv.Atoi(args[1])
			if err != nil || page < 1 {
				return "Usage: /memory review [page]", nil
			}
		}
		if len(candidates) == 0 {
			return "No pending memory candidates.", nil
		}
		pages := (len(candidates) + permission.MaxPopupItems - 1) / permission.MaxPopupItems
		if page > pages {
			return fmt.Sprintf("Memory review has %d page(s).", pages), nil
		}
		start := (page - 1) * permission.MaxPopupItems
		end := start + permission.MaxPopupItems
		if end > len(candidates) {
			end = len(candidates)
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Memory review · page %d of %d · %d pending\n", page, pages, len(candidates))
		for _, rec := range candidates[start:end] {
			fmt.Fprintf(&b, "%s · agent=%v session=%s date=%s\n%s\n", rec.ID, rec.ExtMeta["provider"], rec.SessionID, rec.ObservedAt.Format(time.RFC3339), hideMarshalProtocol(rec.Body))
		}
		if end < len(candidates) {
			fmt.Fprintf(&b, "and %d more · /memory review %d\n", len(candidates)-end, page+1)
		}
		b.WriteString("Each entry requires /memory allow <id> or /memory deny <id>; pending entries are not stored.\n")
		return b.String(), nil
	}
	if len(args) == 2 && (args[0] == "allow" || args[0] == "deny" || args[0] == "request") {
		for _, rec := range candidates {
			if rec.ID == args[1] {
				req := memoryPermission(rec)
				if args[0] == "request" {
					h.ws.queuePermission(req)
					return "Memory permission queued.", nil
				}
				if err := h.ws.decidePermission(ctx, req, args[0] == "allow", "operator memory review"); err != nil {
					return "", err
				}
				return "Memory decision recorded: " + rec.ID, nil
			}
		}
	}
	return "Usage: /memory review | /memory <request|allow|deny> <candidate-id>", nil
}

func memoryPermission(rec model.MemoryRecordV2) permission.Request {
	return permission.Request{Kind: "memory", Object: rec.ID, Scope: "project memory, persistent", Who: "Marshal", Reason: fmt.Sprintf("Retain candidate from %v, session %s, date %s: %s", rec.ExtMeta["provider"], rec.SessionID, rec.ObservedAt.Format(time.RFC3339), hideMarshalProtocol(rec.Body))}
}

func (w *Workspace) guardHistoryWatch(watch *nativeHistoryWatch, provider string) {
	if watch.openCodeDB != "" {
		watch.dir = filepath.Dir(watch.openCodeDB)
	}

	watch.authorized = func(path string) bool {
		if watch.antigravity {
			_, err := os.Stat(watch.antigravitySummaries)
			return w.runtime.HasReadGrant(path) && (os.IsNotExist(err) || w.runtime.HasReadGrant(watch.antigravitySummaries))
		}
		return w.runtime.HasReadGrant(path)
	}
	if watch.antigravity && !w.runtime.HasReadGrant(watch.antigravitySummaries) {
		w.queuePermission(permission.Request{Kind: "read", Object: watch.antigravitySummaries, Scope: "this session only, read-only", Who: "Marshal", Reason: "Read project ownership metadata for Antigravity conversations"})
	}
	if watch.dir != "" && !w.runtime.HasReadGrant(watch.dir) {
		w.queuePermission(permission.Request{Kind: "read", Object: watch.dir, Scope: "this session only, read-only", Who: "Marshal", Reason: "Read project-filtered " + provider + " history for the shared channel and memory candidates"})
	}
}
