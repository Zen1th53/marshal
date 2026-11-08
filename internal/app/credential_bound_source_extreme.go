//go:build !governed

package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/proposalmcp"
)

func (r *Runtime) checkBoundCredentialSource(ctx context.Context, req permission.Request, allow bool) error {
	if req.SourceLaunch == "" && req.SourceOccurrence == "" {
		// The authenticated owner local-control surface may decide its own
		// parent-minted request. A provider child cannot obtain that context;
		// model decisions instead carry both source fields and must prove the
		// exact live MCP receipt below.
		return nil
	}
	if req.SourceLaunch == "" || req.SourceOccurrence == "" {
		return errors.New("incomplete model decision source")
	}
	p, err := r.store.MCPProposal(ctx, r.ProjectID(), req.SourceOccurrence)
	if err != nil || p.ID != req.SourceOccurrence || p.Launch != req.SourceLaunch || p.State != "applying" {
		return errors.New("model decision source is not applying")
	}
	journal, err := r.store.PendingMarshalProposals(ctx, r.ProjectID())
	if err != nil {
		return err
	}
	bound := false
	for _, row := range journal {
		if row.ID != p.ID {
			continue
		}
		var exact permission.Request
		if json.Unmarshal([]byte(row.RequestJSON), &exact) == nil && exact == req {
			bound = true
		}
		break
	}
	if !bound {
		return errors.New("model decision has no exact accepted request journal")
	}
	sum := sha256.Sum256([]byte(p.Payload))
	if p.Digest != hex.EncodeToString(sum[:]) {
		return errors.New("model decision payload changed")
	}
	var answer struct {
		Action string `json:"action"`
		ID     string `json:"id"`
		Allow  bool   `json:"allow"`
		Reason string `json:"reason"`
	}
	if err := proposalmcp.DecodeStrict(json.RawMessage(p.Payload), &answer); err != nil || answer.Action != "decide" {
		return errors.New("model decision source is malformed")
	}
	metadata, ok := ctx.Value(extremeDecisionKey{}).(map[string]any)
	if !ok || metadata["request_id"] != answer.ID || metadata["decision_reason"] != answer.Reason || metadata["approval_binding"] != req.ApprovalBinding || answer.Allow != allow {
		return errors.New("model decision source does not match request")
	}
	return nil
}
