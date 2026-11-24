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

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/proposalmcp"
	"github.com/Zen1th53/marshal/internal/store"
)

type proposalMCPState struct {
	mu     sync.Mutex
	leases map[*briefingDir]*proposalMCPLease
	active *proposalMCPLease
	closed bool
	// predecessor is the lease of a Marshal chat the Board is restarting:
	// revoked (it answers nothing), its open submissions kept for the
	// launch that replaces it. Nil when no restart is in flight.
	predecessor *proposalMCPPredecessor
}

// proposalMCPPredecessor is the identity of a retired Marshal chat lease.
type proposalMCPPredecessor struct {
	id, owner, provider string
	at                  time.Time
}

// proposalMCPRebindWindow bounds how long a restarted chat's submissions
// wait for the launch that replaces it.
const proposalMCPRebindWindow = 10 * time.Minute

type proposalMCPLease struct {
	recoveryRefusals             map[string]bool
	designReviews                map[string]*designReviewJob
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
	OccurrenceID string                   `json:"occurrence_id"`
	State        string                   `json:"state"`
	Reason       string                   `json:"reason,omitempty"`
	Code         string                   `json:"code,omitempty"`
	Blocker      *marshal.OperationHolder `json:"blocker,omitempty"`
	Effects      marshal.RefusalEffects   `json:"effects,omitempty"`
	NextCommands []string                 `json:"nextCommands,omitempty"`
	EscalationID string                   `json:"escalationId,omitempty"`
	Attempt      int                      `json:"attempt,omitempty"`
}

func (w *Workspace) marshalProposalMCPReceipt(ctx context.Context, p store.MCPProposal) (json.RawMessage, error) {
	rec := proposalMCPReceipt{
		OccurrenceID: p.ID,
		State:        p.State,
		Reason:       p.Reason,
	}
	if receipt, ok := w.loadProposalRefusalReceipt(ctx, p.ID); ok {
		rec.Code = receipt.Code
		rec.Blocker = receipt.Blocker
		rec.Effects = receipt.Effects
		rec.NextCommands = receipt.NextCommands
		rec.EscalationID = receipt.EscalationID
		rec.Attempt = receipt.Attempt
	} else if p.State == "rejected" && p.Reason != "" {
		if receipt, ok := marshal.ParseRefusalSummary(p.Reason); ok {
			rec.Code = receipt.Code
			rec.Blocker = receipt.Blocker
			rec.Effects = receipt.Effects
			rec.NextCommands = receipt.NextCommands
			rec.EscalationID = receipt.EscalationID
			rec.Attempt = receipt.Attempt
		}
	}
	return json.Marshal(rec)
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
	if (provider == "agy" || provider == "antigravity") && !antigravityWorkspaceMCPReady(dir) {
		// Without MARSHAL's server attached (the project has its own
		// .agents/mcp_config.json), agy cannot call the proposal tools.
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
	cfg, closeServer, err := proposalmcp.ListenWith(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		return w.handleProposalMCP(ctx, lease, method, raw)
	}, proposalMCPListenOptions(provider, expected))
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
	filtered := make([]string, 0, len(env))
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

// proposalMCPListenOptions is how the broker for provider's Marshal chat
// listens. Codex starts Marshal's stdio MCP child inside the chat's own
// sandbox, which refuses connect(2) on every Unix socket ("proposalmcp:
// dial: ... operation not permitted"), so the broker also serves a spool in
// .marshal/proposals: a directory the Marshal chat's Codex sandbox already
// lets it write (marshalWritableDirs, and the project under workspace-write).
// The sandbox itself is not widened.
func proposalMCPListenOptions(provider, root string) proposalmcp.Options {
	if canonicalNeutralProvider(provider) != "codex" {
		return proposalmcp.Options{}
	}
	return proposalmcp.Options{SpoolParent: statepath.Path(root, "proposals")}
}

func (w *Workspace) activateProposalMCP(a *activeTmuxAgent) {
	w.proposalMCP.mu.Lock()
	var superseded *proposalMCPLease
	var rebound *proposalMCPRebind
	defer func() {
		w.proposalMCP.mu.Unlock()
		if superseded != nil {
			superseded.cleanupOnce.Do(superseded.cleanup)
		}
		if rebound != nil {
			w.rebindMarshalChatRequests(*rebound)
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
	superseded, rebound = w.takeRebindSourceLocked(l)
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
	for _, job := range l.designReviews {
		job.cancel()
	}
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
	if pred := w.proposalMCP.predecessor; pred != nil && w.store != nil {
		_ = w.store.ExpireMCPLaunch(context.Background(), w.projectID, pred.id)
	}
	w.proposalMCP.predecessor = nil
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
		return proposalAuthRefusal(errors.New("proposal launch is inactive"), "/marshal chat")
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
			return proposalAuthRefusal(errors.New("proposal launch replaced or session differs"), "/marshal chat")
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
		return proposalAuthRefusal(errors.New("proposal launch replaced or session differs"), "/marshal chat")
	}
	return nil
}

// handleProposalMCP serves one broker call of the live Marshal and notes it
// on the Marshal's steps (boardv2_steps.go) once the broker's mutex is
// released, whatever it answers. A call on a lease that is not live is
// refused and noted nowhere.
func (w *Workspace) handleProposalMCP(ctx context.Context, l *proposalMCPLease, method string, raw json.RawMessage) (json.RawMessage, error) {
	out, live, err := w.serveProposalMCP(ctx, l, method, raw)
	if live {
		w.noteBrokerStep(l.id, l.provider, method, raw, out, err)
		if err != nil {
			w.recordAdapterLimitError(ctx, "", method, err)
		}
	}
	return out, err
}

func (w *Workspace) serveProposalMCP(ctx context.Context, l *proposalMCPLease, method string, raw json.RawMessage) (json.RawMessage, bool, error) {
	w.proposalMCP.mu.Lock()
	defer w.proposalMCP.mu.Unlock()
	if err := w.validateProposalLease(l); err != nil {
		return nil, false, err
	}
	out, err := w.serveLiveProposalMCP(ctx, l, method, raw)
	return out, true, err
}

// serveLiveProposalMCP is a broker call on a validated live lease. Caller
// holds proposalMCP.mu.
func (w *Workspace) serveLiveProposalMCP(ctx context.Context, l *proposalMCPLease, method string, raw json.RawMessage) (json.RawMessage, error) {
	if method == "design_review" {
		return w.designReviewMCP(ctx, l, raw)
	}
	if strings.HasPrefix(method, "project_contract_") {
		return w.projectContractMCP(ctx, l, method, raw)
	}
	if method == "architecture_request_answer" {
		return w.answerArchitectureRequestMCP(l, raw)
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
			return nil, proposalParseRefusal(errors.New("invalid submission id"))
		}
		p, err := w.store.MCPProposal(ctx, w.projectID, w.liveOccurrenceMCP(ctx, l, args.SubmissionID))
		if err != nil {
			return nil, err
		}
		return w.marshalProposalMCPReceipt(ctx, p)
	}
	if method != "submit" {
		return nil, proposalParseRefusal(errors.New("unknown proposal method"))
	}
	var args proposalMCPSubmission
	if err := proposalmcp.DecodeStrict(raw, &args); err != nil {
		return nil, proposalParseRefusal(err)
	}
	if !validProposalID(args.SubmissionID) || len(args.Proposal) > 4096 {
		return nil, proposalParseRefusal(errors.New("invalid proposal submission"))
	}
	id := w.liveOccurrenceMCP(ctx, l, args.SubmissionID)
	if existing, err := w.store.MCPProposal(ctx, w.projectID, id); err == nil {
		sum := sha256.Sum256(args.Proposal)
		if existing.Digest != hex.EncodeToString(sum[:]) {
			return nil, proposalStaleBindingRefusal(errors.New("submission id binds another payload"))
		}
		if existing.State != "admitted" {
			return w.marshalProposalMCPReceipt(ctx, existing)
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	var probe struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(args.Proposal, &probe); err != nil {
		return nil, proposalParseRefusal(err)
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
			return nil, proposalParseRefusal(err)
		}
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(args.Proposal, &fields)
		if len(fields) != 4 || string(fields["allow"]) == "null" || fields["allow"] == nil || decision.ID == "" || strings.TrimSpace(decision.Reason) == "" || len(decision.Reason) > 500 || strings.ContainsFunc(decision.Reason, unicode.IsControl) {
			return nil, proposalParseRefusal(errors.New("invalid decision: require exactly action=decide, nonempty id, boolean allow, and nonblank reason of at most 500 UTF-8 bytes with no control characters"))
		}
		w.permissions.mu.Lock()
		pending = w.permissions.build.pending[decision.ID]
		valid := pending != nil && pending.launchID == l.id && pending.answer == nil && time.Now().Before(pending.deadline)
		w.permissions.mu.Unlock()
		if !valid {
			return nil, proposalStaleBindingRefusal(errors.New("that question is no longer open: it was already answered, it expired, or it belongs to an earlier chat; read the inbox for the current question"))
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
						return nil, proposalAuthRefusal(errors.New("broker memory requires its original checked submission and separate decision"), "/marshal status")
					} else if !errors.Is(err, sql.ErrNoRows) {
						return nil, errors.New("memory source journal unavailable")
					}
					found = true
					break
				}
			}
			if !found {
				return nil, proposalStaleBindingRefusal(errors.New("memory candidate does not exist in the live runtime"))
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
				staleErr := proposalStaleBindingRefusal(errors.New("decision no longer pending"))
				w.saveProposalRefusalReceipt(ctx, id, staleErr)
				if err := w.store.SetMCPProposalState(ctx, w.projectID, id, "rejected", staleErr.Error()); err != nil {
					return nil, err
				}
				return nil, staleErr
			}
			if err = w.store.AcceptMCPDecision(ctx, w.projectID, id, string(requestJSON), bound.Key()); err != nil {
				w.permissions.mu.Unlock()
				return nil, err
			}
			pending.answer = &decision.Allow
			pending.reason = decision.Reason
			pending.occurrence = id
			w.permissions.mu.Unlock()
			w.chatRequestAnswered(decision.ID, decision.Allow, time.Now())
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
					reason := "canonical proposal outcome: " + outcome
					if state == "rejected" {
						if receipt, ok := w.loadProposalRefusalReceipt(ctx, id); ok {
							reason = RedactContent(receipt.Summary(), nil)
						}
					}
					if err = w.store.SetMCPProposalState(ctx, w.projectID, id, state, reason); err != nil {
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
	return w.marshalProposalMCPReceipt(ctx, p)
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

// settledProposalMCP reports whether req is bound to a live MCP proposal whose
// application already started or ended. Such a request can never be applied
// again (beginProposalMCPApply refuses it), so queueing it a second time, as a
// proposal recovery racing the first application does, only adds a second
// "Marshal allowed" Activity line reading "application failed".
func (w *Workspace) settledProposalMCP(req permission.Request) bool {
	if req.SourceLaunch == "" || req.SourceOccurrence == "" || w.store == nil {
		return false
	}
	p, err := w.store.MCPProposal(context.Background(), w.projectID, req.SourceOccurrence)
	return err == nil && p.State != "admitted" && p.State != "answer_accepted"
}

// errProposalSettled refuses a second decision on a live MCP proposal whose
// application already started or ended: a duplicate delivery of the same
// proposal. The first decision owns the outcome and its Activity line.
var errProposalSettled = errors.New("proposal receipt is not pending")

func (w *Workspace) beginProposalMCPApply(req permission.Request) (func(bool, error) error, error) {
	if req.SourceLaunch == "" {
		return func(bool, error) error { return nil }, nil
	}
	w.proposalMCP.mu.Lock()
	l := w.proposalMCP.active
	if err := w.validateProposalLease(l); err != nil || l.id != req.SourceLaunch {
		w.proposalMCP.mu.Unlock()
		return nil, proposalAuthRefusal(errors.New("proposal permission belongs to inactive launch"), "/marshal chat")
	}
	p, err := w.store.MCPProposal(context.Background(), w.projectID, req.SourceOccurrence)
	if err == nil && p.Launch == l.id && p.State != "admitted" && p.State != "answer_accepted" {
		w.proposalMCP.mu.Unlock()
		return nil, errProposalSettled
	}
	if err != nil || p.Launch != l.id {
		w.proposalMCP.mu.Unlock()
		return nil, proposalStaleBindingRefusal(errors.New("proposal receipt is not pending"))
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
			reason = RedactContent(applyErr.Error(), nil)
			w.saveProposalRefusalReceipt(context.Background(), p.ID, applyErr)
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

// chatLaunchBound reports whether launch is the live, validated MCP launch
// of the tmux Marshal chat now running.
func (w *Workspace) chatLaunchBound(launch string) bool {
	return launch != "" && w.proposalLaunchID() == launch
}

// activateNativeProposalMCP makes the lease prepared for a native chat
// launch the active Marshal launch, as activateProposalMCP does for the
// tmux chat.
func (w *Workspace) activateNativeProposalMCP(dir *briefingDir) {
	w.proposalMCP.mu.Lock()
	var superseded *proposalMCPLease
	var rebound *proposalMCPRebind
	defer func() {
		w.proposalMCP.mu.Unlock()
		if superseded != nil {
			superseded.cleanupOnce.Do(superseded.cleanup)
		}
		if rebound != nil {
			w.rebindMarshalChatRequests(*rebound)
		}
	}()
	l := w.proposalMCP.leases[dir]
	if l == nil || l.revoked || w.proposalMCP.closed {
		return
	}
	if _, ok := w.nativeChatBound(dir, l.provider); !ok {
		return
	}
	superseded, rebound = w.takeRebindSourceLocked(l)
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

// antigravityWorkspaceMCP reports whether this agy session has MARSHAL's MCP
// server attached through the workspace file it created.
func antigravityWorkspaceMCPReady(dir *briefingDir) bool {
	return dir != nil && dir.workspaceMCPAttached
}
