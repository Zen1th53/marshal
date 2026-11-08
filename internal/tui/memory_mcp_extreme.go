//go:build !governed

package tui

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/proposalmcp"
	"github.com/Zen1th53/marshal/internal/store"
	"strings"
	"time"
	"unicode"
)

// Historical source references are untrusted claims. Launch/provider/session
// identity is supplied only by the authenticated parent, never these fields.
type memoryMCPSource struct {
	Path       string `json:"path,omitempty"`
	Commit     string `json:"commit,omitempty"`
	Session    string `json:"session,omitempty"`
	ObservedAt string `json:"observed_at,omitempty"`
	SHA256     string `json:"sha256,omitempty"`
}
type memoryMCPSubmission struct {
	SubmissionID string            `json:"submission_id,omitempty"`
	ProjectID    string            `json:"project_id,omitempty"`
	Title        string            `json:"title"`
	Body         string            `json:"body"`
	Sources      []memoryMCPSource `json:"sources,omitempty"`
}
type memoryMCPReceipt struct {
	OccurrenceID  string `json:"occurrence_id"`
	MemoryID      string `json:"memory_id"`
	State         string `json:"state"`
	Persisted     bool   `json:"persisted"`
	Indexed       bool   `json:"indexed"`
	PayloadDigest string `json:"payload_digest"`
	ContentDigest string `json:"content_digest,omitempty"`
	Reason        string `json:"reason,omitempty"`
}

// Recovery of a live memory submission uses its checked broker snapshot, not
// the generic file-proposal requester downgrade (which changes the queue key).
// A prefix or requester label alone never establishes this source identity.
func (w *Workspace) recoverBoundMCPMemory(record store.MarshalProposal) bool {
	if !strings.HasPrefix(record.ID, "mcp:") || w.store == nil || w.runtime == nil {
		return false
	}
	w.proposalMCP.mu.Lock()
	defer w.proposalMCP.mu.Unlock()
	l := w.proposalMCP.active
	if w.validateProposalLease(l) != nil {
		return false
	}
	p, err := w.store.MCPProposal(context.Background(), w.projectID, record.ID)
	if err != nil || p.Launch != l.id || p.Owner != l.owner || p.Provider != l.provider {
		return false
	}
	sum := sha256.Sum256([]byte(p.Payload))
	if p.Digest != hex.EncodeToString(sum[:]) || record.Line != marshalProposalPrefix+p.Payload {
		return false
	}
	var action struct {
		Action string `json:"action"`
	}
	if json.Unmarshal([]byte(p.Payload), &action) != nil {
		return false
	}
	if action.Action == "decide" {
		// A model answer is consumed only through the exact live pending request.
		// Replaying its stored permission request would ask for a second answer.
		return true
	}
	if action.Action == "project-contract" {
		if p.State != "admitted" {
			return true
		}
		req, err := w.runtime.RegisterMCPProjectContract(context.Background(), p.ID)
		if err != nil {
			w.RecordActivity("Live MCP project contract recovery refused: checked draft differs")
			return true
		}
		requestJSON, _ := json.Marshal(req)
		if record.RequestJSON != "" && record.RequestJSON != string(requestJSON) {
			w.RecordActivity("Live MCP project contract recovery refused: exact request differs")
			return true
		}
		if _, err := w.resumeProjectContractMCP(context.Background(), l, p); err != nil {
			w.RecordActivity("Live MCP project contract recovery pending; retry the checked submission")
		}
		return true
	}
	if action.Action != "memory" {
		return false
	}
	c, err := w.store.MCPMemory(context.Background(), w.projectID, record.ID)
	parsed, parseErr := parseMarshalProposal(record.Line)
	if err != nil || parseErr != nil || parsed.id != c.Record.ID || c.Record.ProjectID != w.projectID || c.Record.Source.Kind != "live_mcp" || c.Record.Source.Reference != l.owner || c.Record.SessionID != l.session || c.Record.ExtMeta["launch_id"] != l.id || c.Record.ExtMeta["provider"] != l.provider || c.Record.ExtMeta["canonical_project_root"] != w.runtime.ProjectRoot() {
		w.RecordActivity("Live MCP memory recovery refused: source snapshot differs")
		return true
	}
	req := memoryPermission(c.Record)
	req.ProposalID, req.SourceOccurrence, req.SourceLaunch, req.Who = p.ID, p.ID, l.id, "Marshal"
	requestJSON, _ := json.Marshal(req)
	if record.RequestJSON != "" && record.RequestJSON != string(requestJSON) {
		w.RecordActivity("Live MCP memory recovery refused: bound request differs")
		return true
	}
	if _, err := w.resumeMemoryMCP(context.Background(), l, c); err != nil {
		w.RecordActivity("Live MCP memory recovery pending; checked submission retry required")
	}
	return true
}

func validMemoryMCPText(s string, limit int, multiline bool) bool {
	return len(s) > 0 && len(s) <= limit && !strings.ContainsFunc(s, func(r rune) bool { return unicode.IsControl(r) && !(multiline && (r == '\n' || r == '\t')) })
}
func (w *Workspace) submitMemoryMCP(ctx context.Context, l *proposalMCPLease, raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) > 10<<10 {
		return nil, errors.New("memory submission too large")
	}
	var args memoryMCPSubmission
	if err := proposalmcp.DecodeStrict(raw, &args); err != nil {
		return nil, errors.New("invalid memory JSON arguments")
	}
	if (args.ProjectID != "" && args.ProjectID != w.projectID) || !validMemoryMCPText(args.Title, 256, false) || !validMemoryMCPText(args.Body, 4096, true) || len(args.Sources) > 8 {
		return nil, errors.New("invalid memory scope or content bounds")
	}
	for _, src := range args.Sources {
		for _, v := range []string{src.Path, src.Commit, src.Session, src.ObservedAt, src.SHA256} {
			if v != "" && !validMemoryMCPText(v, 512, false) {
				return nil, errors.New("invalid source reference")
			}
		}
		if src.ObservedAt != "" {
			if _, err := time.Parse(time.RFC3339, src.ObservedAt); err != nil {
				return nil, errors.New("invalid source observation time")
			}
		}
		if src.SHA256 != "" {
			if b, err := hex.DecodeString(src.SHA256); err != nil || len(b) != 32 {
				return nil, errors.New("invalid source digest")
			}
		}
	}
	// Stable semantic payload supports fresh-child/fresh-launch reconnect while
	// same-launch raw proposal methods continue to bind their exact bytes.
	data, err := json.Marshal(struct {
		Title, Body string
		Sources     []memoryMCPSource
	}{args.Title, args.Body, args.Sources})
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	if args.SubmissionID == "" {
		args.SubmissionID = "memory-" + digest[:32]
	}
	if !validProposalID(args.SubmissionID) {
		return nil, errors.New("invalid memory submission id")
	}
	if err = w.store.InitMCPMemory(ctx); err != nil {
		return nil, err
	}
	id := occurrenceMCP(l.id, args.SubmissionID)
	if old, e := w.store.MCPMemory(ctx, w.projectID, id); e == nil {
		if old.PayloadDigest != digest {
			return nil, errors.New("submission key binds another memory payload")
		}
		return w.resumeMemoryMCP(ctx, l, old)
	} else if !errors.Is(e, sql.ErrNoRows) {
		return nil, e
	}
	if old, e := w.store.FindMCPMemory(ctx, w.projectID, args.SubmissionID, digest); e == nil && (old.State == "committed" || old.State == "applied") {
		return w.encodeMemoryReceiptMCP(ctx, old)
	} else if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return nil, e
	}
	memorySum := sha256.Sum256([]byte(id))
	now := time.Now().UTC()
	rec := model.MemoryRecordV2{ID: "MEM-" + hex.EncodeToString(memorySum[:16]), ProjectID: w.projectID, Kind: model.MemoryKindSemantic, Lifecycle: model.MemoryCandidate, Confidence: model.ConfidenceInferred, Authority: model.AuthorityAgent, Title: args.Title, Body: args.Body, Scope: string(model.ScopeProject), ScopeID: w.projectID, ObservedAt: now, IngestedAt: now, ValidFrom: now, CreatedAt: now, UpdatedAt: now, SessionID: l.session, Source: model.MemorySource{Kind: "live_mcp", Reference: l.owner}, ExtMeta: map[string]any{"provider": l.provider, "launch_id": l.id, "source_claims": args.Sources, "payload_digest": digest, "canonical_project_root": w.runtime.ProjectRoot()}}
	proposal, _ := json.Marshal(map[string]string{"action": "memory", "id": rec.ID})
	c, _, err := w.store.AdmitMCPMemory(ctx, w.projectID, store.MCPProposal{ID: id, Launch: l.id, Owner: l.owner, Provider: l.provider, Submission: args.SubmissionID, Payload: string(proposal)}, store.MCPMemoryCandidate{ImportKey: args.SubmissionID, PayloadDigest: digest, Record: rec})
	if err != nil {
		return nil, err
	}
	return w.resumeMemoryMCP(ctx, l, c)
}
func (w *Workspace) resumeMemoryMCP(ctx context.Context, l *proposalMCPLease, c store.MCPMemoryCandidate) (json.RawMessage, error) {
	if c.State == "admitted" {
		p, err := w.store.MCPProposal(ctx, w.projectID, c.Occurrence)
		if err != nil {
			return nil, err
		}
		if p.Launch != l.id {
			return nil, errors.New("pending memory belongs to another launch")
		}
		rec, err := w.runtime.RegisterMCPMemoryCandidate(ctx, c.Occurrence)
		if err != nil {
			return nil, err
		}
		req := memoryPermission(rec)
		req.ProposalID = c.Occurrence
		req.Who = "Marshal"
		if err = w.queuePermissionChecked(req); err != nil {
			return nil, fmt.Errorf("memory candidate admitted but queue failed; retry same submission: %w", err)
		}
	}
	return w.encodeMemoryReceiptMCP(ctx, c)
}
func (w *Workspace) encodeMemoryReceiptMCP(ctx context.Context, c store.MCPMemoryCandidate) (json.RawMessage, error) {
	// Reconciliation uses committed canonical evidence; no approval or effect replay.
	if c.State == "committed" {
		if err := w.runtime.RepairMCPMemoryIndex(ctx, c.Occurrence); err == nil {
			c.State = "applied"
		}
	}
	r := memoryMCPReceipt{OccurrenceID: c.Occurrence, MemoryID: c.Record.ID, State: c.State, PayloadDigest: c.PayloadDigest, Persisted: c.State == "committed" || c.State == "applied", Indexed: c.State == "applied"}
	if c.State == "admitted" || c.State == "applying" {
		r.State = "pending_permission"
		r.Reason = "queued for direct runtime application; no decision turn is needed, re-read this receipt"
	}
	if c.State == "committed" {
		r.Reason = "canonical memory retained; index repair pending"
	}
	if r.Persisted {
		rec, err := w.store.GetMemoryV2(ctx, w.projectID, c.Record.ID)
		if err != nil {
			return nil, err
		}
		r.ContentDigest = rec.ContentDigest
	}
	return json.Marshal(r)
}
func (w *Workspace) memoryReceiptMCP(ctx context.Context, l *proposalMCPLease, raw json.RawMessage) (json.RawMessage, error) {
	var args struct {
		SubmissionID string `json:"submission_id"`
	}
	if err := proposalmcp.DecodeStrict(raw, &args); err != nil {
		return nil, errors.New("invalid memory JSON arguments")
	}
	if !validProposalID(args.SubmissionID) {
		return nil, errors.New("invalid submission id")
	}
	if err := w.store.InitMCPMemory(ctx); err != nil {
		return nil, err
	}
	c, err := w.store.MCPMemory(ctx, w.projectID, occurrenceMCP(l.id, args.SubmissionID))
	if errors.Is(err, sql.ErrNoRows) {
		c, err = w.store.FindMCPMemory(ctx, w.projectID, args.SubmissionID, "")
	}
	if err != nil {
		return nil, err
	}
	return w.encodeMemoryReceiptMCP(ctx, c)
}
