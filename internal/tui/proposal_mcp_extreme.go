//go:build !governed

package tui

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/Zen1th53/marshal/internal/statepath"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/proposalmcp"
	"github.com/Zen1th53/marshal/internal/store"
)

type proposalMCPState struct {
	mu     sync.Mutex
	leases map[*briefingDir]*proposalMCPLease
	active *proposalMCPLease
	closed bool
}
type proposalMCPLease struct {
	planning                     *nativePlanningLeaseRead
	id, owner, provider, session string
	dir                          *briefingDir
	active, revoked              bool
	native                       bool // bound to the Board's native chat, not a tmux pane
	cleanup                      func()
	cleanupOnce                  sync.Once
}
type proposalMCPSubmission struct {
	SubmissionID string          `json:"submission_id"`
	Proposal     json.RawMessage `json:"proposal"`
}
type proposalMCPReceipt struct {
	OccurrenceID string `json:"occurrence_id"`
	State        string `json:"state"`
	Reason       string `json:"reason,omitempty"`
}

func randomProposalID() (string, error) {
	var b [32]byte
	_, err := rand.Read(b[:])
	return hex.EncodeToString(b[:]), err
}
func validProposalID(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}
func occurrenceMCP(launch, submission string) string {
	sum := sha256.Sum256([]byte(launch + "\x00" + submission))
	return "mcp:" + hex.EncodeToString(sum[:])
}
func (w *Workspace) prepareProposalMCP(provider, root string, args, env []string, dir *briefingDir) ([]string, []string, *briefingDir, error) {
	if provider == "agy" || provider == "antigravity" {
		var err error
		args, err = w.prepareUnleasedNativePlanningLaunch(context.Background(), provider, root, args, dir)
		return args, env, dir, err
	}
	if w.store == nil || w.projectID == "" || w.runtime == nil {
		// Read-only MCP launch remains available; no live authority is minted.
		return args, env, dir, nil
	}
	actual, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, nil, dir, err
	}
	expected, err := filepath.EvalSymlinks(w.runtime.ProjectRoot())
	if err != nil {
		return nil, nil, dir, err
	}
	if actual != expected || w.runtime.ProjectID() != w.projectID || w.runtime.StateDirectory() != statepath.Path(expected) {
		return nil, nil, dir, errors.New("proposal MCP project binding differs")
	}
	if err := w.store.InitMCPProposals(context.Background()); err != nil {
		return nil, nil, dir, err
	}
	if dir == nil {
		var err error
		dir, err = newBriefingDir(root, provider)
		if err != nil {
			return nil, nil, dir, err
		}
	}
	id, err := randomProposalID()
	if err != nil {
		return nil, nil, dir, err
	}
	owner, err := randomProposalID()
	if err != nil {
		return nil, nil, dir, err
	}
	lease := &proposalMCPLease{id: id, owner: owner, provider: canonicalNeutralProvider(provider), dir: dir}
	args, err = w.prepareNativePlanningLaunch(context.Background(), lease, root, args, dir)
	if err != nil {
		return nil, nil, dir, err
	}
	cfg, closeServer, err := proposalmcp.Listen(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		return w.handleProposalMCP(ctx, lease, method, raw)
	})
	if err != nil {
		return nil, nil, dir, err
	}
	cfg.Namespace = statepath.DirName
	cfg.ProjectID = w.projectID
	cfg.ProjectRoot = expected
	path, removeConfig, err := proposalmcp.SaveConfig(cfg)
	if err != nil {
		closeServer()
		return nil, nil, dir, err
	}
	lease.cleanup = func() { closeServer(); removeConfig() }
	w.proposalMCP.mu.Lock()
	if w.proposalMCP.closed {
		w.proposalMCP.mu.Unlock()
		lease.cleanup()
		return nil, nil, dir, errors.New("workspace closed")
	}
	if w.proposalMCP.leases == nil {
		w.proposalMCP.leases = map[*briefingDir]*proposalMCPLease{}
	}
	w.proposalMCP.leases[dir] = lease
	w.proposalMCP.mu.Unlock()
	dir.revokeProposal = func() { w.revokeProposalLease(lease) }
	filtered := make([]string, 0, len(env)+1)
	for _, e := range env {
		if !strings.HasPrefix(e, "MARSHAL_PROPOSAL_MCP_CONFIG=") {
			filtered = append(filtered, e)
		}
	}
	env = append(filtered, "MARSHAL_PROPOSAL_MCP_CONFIG="+path)
	args, env, err = bindMarshalMCPChildEnvironment(provider, root, args, env, dir, path)
	if err != nil {
		w.revokeProposalLease(lease)
		return nil, nil, dir, err
	}
	return args, env, dir, nil
}
func (w *Workspace) activateProposalMCP(a *activeTmuxAgent) {
	w.proposalMCP.mu.Lock()
	var superseded *proposalMCPLease
	defer func() {
		w.proposalMCP.mu.Unlock()
		if superseded != nil {
			superseded.cleanupOnce.Do(superseded.cleanup)
		}
	}()
	l := w.proposalMCP.leases[a.briefingDir]
	if l == nil || l.revoked || w.proposalMCP.closed {
		return
	}
	w.tmuxMu.Lock()
	bound := w.tmuxActiveWins["marshal-chat"] == a && a.role == "marshal-chat" && canonicalNeutralProvider(a.provider) == l.provider && a.state == "working"
	session := a.sessionID
	if bound {
		a.mcpLaunchID = l.id
	}
	w.tmuxMu.Unlock()
	if !bound {
		return
	}
	if old := w.proposalMCP.active; old != nil && old != l {
		old.planning = nil
		old.active = false
		old.revoked = true
		superseded = old
		delete(w.proposalMCP.leases, old.dir)
		_ = w.store.ExpireMCPLaunch(context.Background(), w.projectID, old.id)
	}
	l.active = true
	l.session = session
	w.proposalMCP.active = l
	if w.runtime != nil {
		w.runtime.SetProviderTransportReviewSource(func(launch, owner, provider string) bool {
			w.proposalMCP.mu.Lock()
			defer w.proposalMCP.mu.Unlock()
			active := w.proposalMCP.active
			return active != nil && active.id == launch && active.owner == owner && active.provider == provider && w.validateProposalLease(active) == nil
		})
	}
}
func (w *Workspace) revokeProposalLease(l *proposalMCPLease) {
	w.proposalMCP.mu.Lock()
	if l.revoked {
		w.proposalMCP.mu.Unlock()
		l.cleanupOnce.Do(l.cleanup)
		return
	}
	l.planning = nil
	l.revoked = true
	l.active = false
	if w.proposalMCP.active == l {
		w.proposalMCP.active = nil
	}
	if w.store != nil {
		_ = w.store.ExpireMCPLaunch(context.Background(), w.projectID, l.id)
	}
	delete(w.proposalMCP.leases, l.dir)
	w.proposalMCP.mu.Unlock()
	l.cleanupOnce.Do(l.cleanup)
}
func (w *Workspace) revokeProposalMCP(a *activeTmuxAgent) {
	if a == nil {
		return
	}
	w.proposalMCP.mu.Lock()
	l := w.proposalMCP.leases[a.briefingDir]
	w.proposalMCP.mu.Unlock()
	if l != nil {
		w.revokeProposalLease(l)
	}
}
func (w *Workspace) closeProposalMCP() {
	w.proposalMCP.mu.Lock()
	w.proposalMCP.closed = true
	var leases []*proposalMCPLease
	for _, l := range w.proposalMCP.leases {
		leases = append(leases, l)
	}
	w.proposalMCP.mu.Unlock()
	for _, l := range leases {
		w.revokeProposalLease(l)
	}
}

// Caller holds the authority mutex until canonical admission/effect completes.
func (w *Workspace) validateProposalLease(l *proposalMCPLease) error {
	if l == nil || l.revoked || !l.active || w.proposalMCP.active != l || w.proposalMCP.closed {
		return errors.New("proposal launch is inactive")
	}
	if l.native {
		// The native chat holds no tmux agent; its transport is the launch.
		session, ok := w.nativeChatBound(l.dir, l.provider)
		if ok && l.session != "" && session != "" && session != l.session {
			ok = false
		}
		if ok && l.session == "" {
			l.session = session
		}
		if !ok {
			return errors.New("proposal launch replaced or session differs")
		}
		return nil
	}
	w.tmuxMu.Lock()
	a := w.tmuxActiveWins["marshal-chat"]
	valid := a != nil && a.briefingDir == l.dir && a.role == "marshal-chat" && a.state == "working" && canonicalNeutralProvider(a.provider) == l.provider
	if valid && l.session != "" && a.sessionID != l.session {
		valid = false
	}
	if valid && l.session == "" && a.sessionID != "" {
		l.session = a.sessionID
	}
	w.tmuxMu.Unlock()
	if !valid {
		return errors.New("proposal launch replaced or session differs")
	}
	return nil
}
func (w *Workspace) handleProposalMCP(ctx context.Context, l *proposalMCPLease, method string, raw json.RawMessage) (json.RawMessage, error) {
	w.proposalMCP.mu.Lock()
	defer w.proposalMCP.mu.Unlock()
	if err := w.validateProposalLease(l); err != nil {
		return nil, err
	}
	if strings.HasPrefix(method, "project_contract_") {
		return w.projectContractMCP(ctx, l, method, raw)
	}
	if method == "memory_submit" {
		return w.submitMemoryMCP(ctx, l, raw)
	}
	if method == "memory_receipt" {
		return w.memoryReceiptMCP(ctx, l, raw)
	}
	if method == "receipt" {
		var args struct {
			SubmissionID string `json:"submission_id"`
		}
		if err := proposalmcp.DecodeStrict(raw, &args); err != nil {
			return nil, err
		}
		if !validProposalID(args.SubmissionID) {
			return nil, errors.New("invalid submission id")
		}
		p, err := w.store.MCPProposal(ctx, w.projectID, occurrenceMCP(l.id, args.SubmissionID))
		if err != nil {
			return nil, err
		}
		return json.Marshal(proposalMCPReceipt{p.ID, p.State, p.Reason})
	}
	if method != "submit" {
		return nil, errors.New("unknown proposal method")
	}
	var args proposalMCPSubmission
	if err := proposalmcp.DecodeStrict(raw, &args); err != nil {
		return nil, err
	}
	if !validProposalID(args.SubmissionID) || len(args.Proposal) > 4096 {
		return nil, errors.New("invalid proposal submission")
	}
	id := occurrenceMCP(l.id, args.SubmissionID)
	if existing, err := w.store.MCPProposal(ctx, w.projectID, id); err == nil {
		sum := sha256.Sum256(args.Proposal)
		if existing.Digest != hex.EncodeToString(sum[:]) {
			return nil, errors.New("submission id binds another payload")
		}
		if existing.State != "admitted" {
			return json.Marshal(proposalMCPReceipt{existing.ID, existing.State, existing.Reason})
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	var probe struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(args.Proposal, &probe); err != nil {
		return nil, err
	}
	var parsed marshalProposal
	var decision struct {
		Action string `json:"action"`
		ID     string `json:"id"`
		Allow  bool   `json:"allow"`
		Reason string `json:"reason"`
	}
	var pending *buildPendingDecision
	if probe.Action == "decide" {
		if err := proposalmcp.DecodeStrict(args.Proposal, &decision); err != nil {
			return nil, err
		}
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(args.Proposal, &fields)
		if len(fields) != 4 || string(fields["allow"]) == "null" || fields["allow"] == nil || decision.ID == "" || strings.TrimSpace(decision.Reason) == "" || len(decision.Reason) > 500 || strings.ContainsFunc(decision.Reason, unicode.IsControl) {
			return nil, errors.New("invalid decision: require exactly action=decide, nonempty id, boolean allow, and nonblank reason of at most 500 UTF-8 bytes with no control characters")
		}
		w.permissions.mu.Lock()
		pending = w.permissions.build.pending[decision.ID]
		valid := pending != nil && pending.launchID == l.id && pending.answer == nil && time.Now().Before(pending.deadline)
		w.permissions.mu.Unlock()
		if !valid {
			return nil, errors.New("decision is unknown, answered, expired or belongs to another launch")
		}
	} else {
		var err error
		parsed, err = parseMarshalProposal(marshalProposalPrefix + string(args.Proposal))
		if err != nil {
			return nil, err
		}
		if parsed.action == "memory" {
			found := false
			for _, rec := range w.runtime.ContinuationCandidates() {
				if rec.ID == parsed.id {
					if _, err := w.store.MCPMemoryByRecordID(ctx, w.projectID, rec.ID); err == nil {
						return nil, errors.New("broker memory requires its original checked submission and separate decision")
					} else if !errors.Is(err, sql.ErrNoRows) {
						return nil, errors.New("memory source journal unavailable")
					}
					found = true
					break
				}
			}
			if !found {
				return nil, errors.New("memory candidate does not exist in the live runtime")
			}
		}
	}
	p, fresh, err := w.store.AdmitMCPProposal(ctx, w.projectID, store.MCPProposal{ID: id, Launch: l.id, Owner: l.owner, Provider: l.provider, Submission: args.SubmissionID, Payload: string(args.Proposal)})
	if err != nil {
		return nil, err
	}
	if fresh || p.State == "admitted" {
		if pending != nil {
			bound := pending.request
			bound.SourceLaunch = l.id
			bound.SourceOccurrence = id
			requestJSON, encodeErr := json.Marshal(bound)
			if encodeErr != nil {
				return nil, encodeErr
			}
			w.permissions.mu.Lock()
			current := w.permissions.build.pending[decision.ID]
			if current != pending || current.answer != nil || !time.Now().Before(current.deadline) {
				w.permissions.mu.Unlock()
				if err := w.store.SetMCPProposalState(ctx, w.projectID, id, "rejected", "decision no longer pending"); err != nil {
					return nil, err
				}
				return nil, errors.New("decision no longer pending")
			}
			if err = w.store.AcceptMCPDecision(ctx, w.projectID, id, string(requestJSON), bound.Key()); err != nil {
				w.permissions.mu.Unlock()
				return nil, err
			}
			pending.answer = &decision.Allow
			pending.reason = decision.Reason
			pending.occurrence = id
			w.permissions.mu.Unlock()
		} else {
			parsed.occurrence = id
			parsed.who = "Marshal"
			parsed.sourceSession = l.session
			if parsed.action == "intake" || parsed.action == "ask" || parsed.action == "requirements" {
				if err = w.store.SetMCPProposalState(ctx, w.projectID, id, "applying", parsed.action+" application admitted"); err != nil {
					return nil, err
				}
			}
			w.enqueueValidatedMarshalProposal(parsed)
			if outcome, err := w.store.MCPProposalLegacyOutcome(ctx, w.projectID, id); err != nil {
				return nil, err
			} else {
				if outcome != "" {
					state := "rejected"
					if outcome == "applied" {
						state = "applied"
					}
					if err = w.store.SetMCPProposalState(ctx, w.projectID, id, state, "canonical proposal outcome: "+outcome); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	p, err = w.store.MCPProposal(ctx, w.projectID, id)
	if err != nil {
		return nil, err
	}
	return json.Marshal(proposalMCPReceipt{p.ID, p.State, p.Reason})
}
func (w *Workspace) bindProposalMCPRequest(req permission.Request) (permission.Request, error) {
	if !strings.HasPrefix(req.ProposalID, "mcp:") {
		return req, nil
	}
	p, err := w.store.MCPProposal(context.Background(), w.projectID, req.ProposalID)
	if err != nil {
		return req, err
	}
	req.SourceLaunch = p.Launch
	req.SourceOccurrence = p.ID
	return req, nil
}
func (w *Workspace) beginProposalMCPApply(req permission.Request) (func(bool, error) error, error) {
	if req.SourceLaunch == "" {
		return func(bool, error) error { return nil }, nil
	}
	w.proposalMCP.mu.Lock()
	l := w.proposalMCP.active
	if err := w.validateProposalLease(l); err != nil || l.id != req.SourceLaunch {
		w.proposalMCP.mu.Unlock()
		return nil, errors.New("proposal permission belongs to inactive launch")
	}
	p, err := w.store.MCPProposal(context.Background(), w.projectID, req.SourceOccurrence)
	if err != nil || p.Launch != l.id || (p.State != "admitted" && p.State != "answer_accepted") {
		w.proposalMCP.mu.Unlock()
		return nil, errors.New("proposal receipt is not pending")
	}
	if err = w.store.SetMCPProposalState(context.Background(), w.projectID, p.ID, "applying", "canonical application admitted"); err != nil {
		w.proposalMCP.mu.Unlock()
		return nil, err
	}
	return func(allow bool, applyErr error) error {
		defer w.proposalMCP.mu.Unlock()
		if (req.Kind == "memory" && req.ProposalID == p.ID) || req.Kind == "project-contract" {
			saved, readErr := w.store.MCPProposal(context.Background(), w.projectID, p.ID)
			if readErr == nil && (saved.State == "committed" || saved.State == "applied" || saved.State == "denied") {
				return nil
			}
		}
		state := "denied"
		if allow {
			state = "applied"
		}
		reason := "canonical application completed"
		if applyErr != nil {
			state = "rejected"
			reason = applyErr.Error()
		}
		err := w.store.SetMCPProposalState(context.Background(), w.projectID, p.ID, state, reason)
		return errors.Join(err, w.store.ResolveMarshalProposal(context.Background(), w.projectID, p.ID, state))
	}, nil
}
func (w *Workspace) staleProposalMCP(id string) bool {
	if !strings.HasPrefix(id, "mcp:") {
		return false
	}
	if w.store != nil {
		p, err := w.store.MCPProposal(context.Background(), w.projectID, id)
		if err == nil {
			w.proposalMCP.mu.Lock()
			l := w.proposalMCP.active
			active := l != nil && l.id == p.Launch && w.validateProposalLease(l) == nil
			w.proposalMCP.mu.Unlock()
			if active {
				return false
			}
			_ = w.store.ExpireMCPLaunch(context.Background(), w.projectID, p.Launch)
		}
	}
	return true
}
func (w *Workspace) proposalLaunchID() string {
	w.proposalMCP.mu.Lock()
	defer w.proposalMCP.mu.Unlock()
	if w.validateProposalLease(w.proposalMCP.active) == nil {
		return w.proposalMCP.active.id
	}
	return ""
}

// activateNativeProposalMCP makes the lease prepared for a native chat
// launch the active Marshal launch, as activateProposalMCP does for the
// tmux chat.
func (w *Workspace) activateNativeProposalMCP(dir *briefingDir) {
	w.proposalMCP.mu.Lock()
	var superseded *proposalMCPLease
	defer func() {
		w.proposalMCP.mu.Unlock()
		if superseded != nil {
			superseded.cleanupOnce.Do(superseded.cleanup)
		}
	}()
	l := w.proposalMCP.leases[dir]
	if l == nil || l.revoked || w.proposalMCP.closed {
		return
	}
	if _, ok := w.nativeChatBound(dir, l.provider); !ok {
		return
	}
	if old := w.proposalMCP.active; old != nil && old != l {
		old.planning = nil
		old.active = false
		old.revoked = true
		superseded = old
		delete(w.proposalMCP.leases, old.dir)
		_ = w.store.ExpireMCPLaunch(context.Background(), w.projectID, old.id)
	}
	l.native = true
	l.active = true
	w.proposalMCP.active = l
	if w.runtime != nil {
		w.runtime.SetProviderTransportReviewSource(func(launch, owner, provider string) bool {
			w.proposalMCP.mu.Lock()
			defer w.proposalMCP.mu.Unlock()
			active := w.proposalMCP.active
			return active != nil && active.id == launch && active.owner == owner && active.provider == provider && w.validateProposalLease(active) == nil
		})
	}
}

// revokeNativeProposalMCP ends the lease of a native chat launch.
func (w *Workspace) revokeNativeProposalMCP(dir *briefingDir) {
	if dir == nil {
		return
	}
	w.proposalMCP.mu.Lock()
	l := w.proposalMCP.leases[dir]
	w.proposalMCP.mu.Unlock()
	if l != nil {
		w.revokeProposalLease(l)
	}
}
