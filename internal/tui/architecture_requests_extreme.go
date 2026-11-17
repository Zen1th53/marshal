//go:build !governed

package tui

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/proposalmcp"
)

// answerArchitectureRequestMCP delivers the live Marshal's answer to an
// owner request waiting on the architecture page. The provider and session
// come from the authenticated lease, never from the arguments.
func (w *Workspace) answerArchitectureRequestMCP(l *proposalMCPLease, raw json.RawMessage) (json.RawMessage, error) {
	var args struct {
		RequestID string   `json:"request_id"`
		Status    string   `json:"status"`
		Reply     string   `json:"reply"`
		Questions []string `json:"questions,omitempty"`
	}
	if err := proposalmcp.DecodeStrict(raw, &args); err != nil {
		return nil, err
	}
	if !validProposalID(args.RequestID) {
		return nil, errors.New("request_id must be the id from the inbox entry")
	}
	switch args.Status {
	case "answered", "proposed_change":
		if len(args.Questions) > 0 {
			return nil, errors.New("questions belong only to status needs_clarification")
		}
	case "needs_clarification":
		if len(args.Questions) == 0 {
			return nil, errors.New("needs_clarification requires at least one question")
		}
	default:
		return nil, errors.New("status must be answered, needs_clarification or proposed_change")
	}
	if strings.TrimSpace(args.Reply) == "" || len(args.Reply) > 4000 || !utf8.ValidString(args.Reply) {
		return nil, errors.New("reply must be nonempty text of at most 4000 bytes")
	}
	if len(args.Questions) > 8 {
		return nil, errors.New("at most 8 questions")
	}
	for _, q := range args.Questions {
		if strings.TrimSpace(q) == "" || len(q) > 500 {
			return nil, errors.New("each question must be nonempty text of at most 500 bytes")
		}
	}
	session := l.session
	if session == "" {
		session = l.id
	}
	answer := app.ArchitectureAnswer{Status: args.Status, Reply: args.Reply, Questions: args.Questions, Provider: l.provider, Session: session}
	if !w.architectureRequests.deliver(args.RequestID, answer) {
		return nil, errors.New("no architecture request with this id is waiting; it was answered, withdrawn or timed out")
	}
	return json.RawMessage(`{"delivered":true}`), nil
}
