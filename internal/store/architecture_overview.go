package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// architectureAcceptanceScan bounds how many of the newest owner
// acceptances are looked at for the project's.
const architectureAcceptanceScan = 20

// Records an accepted architecture comes from.
const (
	// ArchitectureRecordContract is the Extreme project contract, accepted
	// by the owner's "accept architecture" instruction.
	ArchitectureRecordContract = "project contract"
	// ArchitectureRecordOwner is the owner's signed architecture
	// acceptance.
	ArchitectureRecordOwner = "architecture"
)

// ArchitectureOverview is a project's accepted architecture for the TUI
// Architecture view: what was accepted, by whom and when, and whether the
// store holds anything newer.
type ArchitectureOverview struct {
	ProjectID string
	// Accepted is the accepted revision in force; nil before one.
	Accepted *ArchitectureAccepted
	// Comparable is set when the store keeps the accepted record's body
	// and can tell whether a newer revision changed it. Current is then the
	// newest stored revision and Changes the top-level fields it changed
	// against the accepted one: none when they are the same revision.
	Comparable bool
	Current    int64
	Changes    []string
}

// ArchitectureAccepted is one accepted architecture revision.
type ArchitectureAccepted struct {
	// Record is ArchitectureRecordContract or ArchitectureRecordOwner.
	Record   string
	ID       string
	Revision int64
	Digest   string
	// By is who accepted it: the owner, or the Marshal when no owner
	// instruction is recorded for the acceptance; Instruction is the
	// owner's words when they are.
	By, Instruction string
	At              time.Time
}

// ArchitectureOverview reads a project's accepted architecture. The
// Extreme project contract is read first, through its per-project active
// pointer and the revisions' primary key; without one, the owner's newest
// signed acceptance is read newest first by its sequence and its audit by
// key. Nothing scans a table with json_extract: bodies are decoded in Go.
func (s *Store) ArchitectureOverview(ctx context.Context, projectID string) (ArchitectureOverview, error) {
	out := ArchitectureOverview{ProjectID: projectID}
	if s == nil || s.db == nil {
		return out, fmt.Errorf("store database is not configured")
	}
	if projectID == "" {
		return out, fmt.Errorf("%w: missing project", model.ErrInvalid)
	}
	if err := s.architectureContract(ctx, projectID, &out); err != nil || out.Accepted != nil {
		return out, err
	}
	return out, s.architectureOwnerAcceptance(ctx, projectID, &out)
}

// architectureOwnerAcceptance reads the project's newest signed owner
// acceptance among the newest few, with its audit row by key.
func (s *Store) architectureOwnerAcceptance(ctx context.Context, projectID string, out *ArchitectureOverview) error {
	rows, err := s.db.QueryContext(ctx, `SELECT r.project_id, r.receipt, a.origin, a.accepted_at FROM architecture_accepted_revisions r
		JOIN architecture_owner_audit a ON a.receipt_id = r.receipt_id ORDER BY r.sequence DESC LIMIT ?`, architectureAcceptanceScan)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var project, origin, at string
		var raw []byte
		if err := rows.Scan(&project, &raw, &origin, &at); err != nil {
			return err
		}
		if project != projectID {
			continue
		}
		var r SignedArchitectureReceipt
		if err := json.Unmarshal(raw, &r); err != nil {
			return fmt.Errorf("decode architecture acceptance: %w", err)
		}
		b := r.Challenge.Binding
		a := &ArchitectureAccepted{Record: ArchitectureRecordOwner, ID: b.ArchitectureID, Revision: b.Revision, Digest: b.Digest, By: "owner", Instruction: r.Challenge.Message}
		if origin == "owner_browser" {
			a.By = "owner in the browser"
		}
		a.At, _ = time.Parse(time.RFC3339Nano, at)
		out.Accepted = a
		break
	}
	return rows.Err()
}

// architectureChanges is the top-level fields of two JSON objects that
// differ, sorted.
func architectureChanges(accepted, current string) ([]string, error) {
	var a, c map[string]json.RawMessage
	if err := json.Unmarshal([]byte(accepted), &a); err != nil {
		return nil, fmt.Errorf("decode accepted contract: %w", err)
	}
	if err := json.Unmarshal([]byte(current), &c); err != nil {
		return nil, fmt.Errorf("decode current contract: %w", err)
	}
	compact := func(v json.RawMessage) string {
		var b bytes.Buffer
		if json.Compact(&b, v) != nil {
			return string(v)
		}
		return b.String()
	}
	var out []string
	for k, v := range a {
		if w, ok := c[k]; !ok || compact(v) != compact(w) {
			out = append(out, k)
		}
	}
	for k := range c {
		if _, ok := a[k]; !ok {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out, nil
}
