package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// ArchitectureRequest is one owner request from a contract-built browser
// review: exactly what the owner selected (by id, with the selected parts'
// full contract entries) and what they asked. Nothing in it is inferred.
type ArchitectureRequest struct {
	ID          string          `json:"request_id"`
	Type        string          `json:"request_type"` // comment or change
	Text        string          `json:"request"`
	Revision    json.Number     `json:"revision"`
	Source      json.RawMessage `json:"source_contract"`
	Anchor      json.RawMessage `json:"anchor"`
	Components  json.RawMessage `json:"components"`
	Connections json.RawMessage `json:"connections"`
}

// ArchitectureAnswer is the Marshal's answer to an ArchitectureRequest. A
// proposed change is the pending project contract the Marshal submitted for
// it; the page reads that contract itself.
type ArchitectureAnswer struct {
	Status    string   `json:"status"` // answered, needs_clarification or proposed_change
	Reply     string   `json:"reply"`
	Questions []string `json:"questions"`
	Provider  string   `json:"provider"`
	Session   string   `json:"session_id"`
}

// errNoNewerContract refuses a proposed change for which the Marshal did not
// submit a revised project contract first.
var errNoNewerContract = errors.New("The Marshal answered with a change but sent no revised project summary; it must submit the revision first.")

// contractFeedback forwards a request on a contract-built page to the live
// Marshal chat and turns its answer into a review result. A proposed change
// becomes the newest pending contract, drawn the same way; the review itself
// still refuses any change outside the owner's selection.
func contractFeedback(forward func(context.Context, ArchitectureRequest) (ArchitectureAnswer, error), pending func(context.Context) (*contractReviewSeed, error)) func(context.Context, json.RawMessage) (json.RawMessage, error) {
	return func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		var envelope struct {
			RequestID    string          `json:"request_id"`
			RequestType  string          `json:"request_type"`
			Request      string          `json:"request"`
			Revision     json.Number     `json:"revision"`
			Architecture json.RawMessage `json:"architecture"`
			Selection    struct {
				Anchor      json.RawMessage `json:"anchor"`
				Components  json.RawMessage `json:"components"`
				Connections json.RawMessage `json:"connections"`
			} `json:"selection"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return nil, err
		}
		var graph struct {
			Source json.RawMessage `json:"source_contract"`
		}
		if err := json.Unmarshal(envelope.Architecture, &graph); err != nil || len(graph.Source) == 0 {
			return nil, errors.New("the page is not built from a project summary")
		}
		answer, err := forward(ctx, ArchitectureRequest{
			ID: envelope.RequestID, Type: envelope.RequestType, Text: envelope.Request, Revision: envelope.Revision,
			Source: graph.Source, Anchor: envelope.Selection.Anchor,
			Components: envelope.Selection.Components, Connections: envelope.Selection.Connections,
		})
		if err != nil {
			return nil, err
		}
		architecture := envelope.Architecture
		if answer.Status == "proposed_change" {
			seed, err := pending(ctx)
			if err != nil {
				return nil, err
			}
			var current contractReviewSource
			if err := json.Unmarshal(graph.Source, &current); err != nil {
				return nil, err
			}
			if seed == nil || seed.Source == current {
				return nil, errNoNewerContract
			}
			architecture = seed.Architecture
		}
		if answer.Questions == nil {
			answer.Questions = []string{}
		}
		result, err := json.Marshal(map[string]any{
			"status": answer.Status, "reply": answer.Reply, "questions": answer.Questions,
			"architecture": architecture, "provider": answer.Provider, "session_id": answer.Session,
		})
		if err != nil {
			return nil, fmt.Errorf("the Marshal answer cannot be read: %w", err)
		}
		return result, nil
	}
}
