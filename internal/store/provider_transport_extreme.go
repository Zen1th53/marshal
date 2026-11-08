//go:build !governed

package store

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Zen1th53/marshal/internal/permission"
)

type MCPTransportReview struct {
	Source  MCPProposal
	Request permission.Request
}

// AppliedMCPTransportReview reads protected native admission and its exact
// accepted authority request. Generic audit events cannot supply this proof.
func (s *Store) AppliedMCPTransportReview(ctx context.Context, project, approvalID string) (MCPTransportReview, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT p.occurrence_id,m.request_json FROM extreme_mcp_proposals p JOIN marshal_proposals m ON m.project_id=p.project_id AND m.occurrence_id=p.occurrence_id WHERE p.project_id=? AND p.state='applied' AND m.outcome='applied' AND json_extract(CASE WHEN json_valid(m.request_json) THEN m.request_json ELSE '{}' END,'$.Kind')='marshal-command' AND json_extract(CASE WHEN json_valid(m.request_json) THEN m.request_json ELSE '{}' END,'$.Object')=? ORDER BY p.rowid DESC`, project, "/marshal approve-task "+approvalID)
	if err != nil {
		return MCPTransportReview{}, err
	}
	defer rows.Close()
	type candidate struct {
		id, launch string
		req        permission.Request
	}
	var candidates []candidate
	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return MCPTransportReview{}, err
		}
		var req permission.Request
		if json.Unmarshal([]byte(raw), &req) == nil && req.Kind == "marshal-command" && req.Object == "/marshal approve-task "+approvalID && req.SourceLaunch != "" && req.SourceOccurrence == id {
			candidates = append(candidates, candidate{id, req.SourceLaunch, req})
		}
	}
	if err := rows.Err(); err != nil {
		return MCPTransportReview{}, err
	}
	rows.Close()
	for _, c := range candidates {
		p, err := s.MCPProposal(ctx, project, c.id)
		if err != nil {
			return MCPTransportReview{}, err
		}
		var answer struct {
			Action string `json:"action"`
			ID     string `json:"id"`
			Allow  bool   `json:"allow"`
		}
		if json.Unmarshal([]byte(p.Payload), &answer) != nil {
			continue
		}
		// The Marshal either answers the runtime's Decision request (decide,
		// allow) or, where its own authenticated proposals apply directly,
		// submits the exact approve-task for this approval itself.
		reviewed := answer.Action == "decide" && answer.ID != "" && answer.Allow || answer.Action == "approve-task" && answer.ID == approvalID
		if reviewed && p.Launch == c.launch && p.Launch != "" && p.Owner != "" && p.Provider != "" {
			return MCPTransportReview{p, c.req}, nil
		}
	}
	return MCPTransportReview{}, fmt.Errorf("exact applied authenticated model transport review absent")
}
