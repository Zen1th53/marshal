package app

import (
	"context"
	"encoding/json"
	"path/filepath"

	"github.com/Zen1th53/marshal/internal/architecturecodec"
	"github.com/Zen1th53/marshal/internal/architecturereview"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/statepath"
	"github.com/Zen1th53/marshal/internal/store"
)

func (r *Runtime) marshalArchitectureSelection(ctx context.Context, raw json.RawMessage) (store.ArchitectureAcceptance, error) {
	var selected struct {
		Project  string `json:"project"`
		ID       string `json:"architecture_id"`
		Revision int64  `json:"revision"`
		Digest   string `json:"digest"`
		Layout   string `json:"layout_digest"`
		UI       string `json:"ui_digest"`
	}
	if !architecturecodec.ModelArchitectureApprovalAllowed() || r == nil || r.store == nil || r.layout.RuntimeDir != statepath.Path(r.layout.Root) {
		return store.ArchitectureAcceptance{}, model.ErrPolicyDenied
	}
	if _, err := architecturecodec.ValidateStoredState(raw, r.ProjectIdentity()); err != nil {
		return store.ArchitectureAcceptance{}, err
	}
	if err := json.Unmarshal(raw, &selected); err != nil {
		return store.ArchitectureAcceptance{}, err
	}
	observed, _, err := r.store.ObserveMarshalArchitecture(ctx)
	if err != nil {
		return store.ArchitectureAcceptance{}, err
	}
	if observed.ProjectID != selected.Project || observed.Root != r.layout.Root || observed.StorePath != filepath.Join(r.layout.RuntimeDir, "state.db") || observed.MatchesReview(selected.ID, selected.Revision, selected.Digest, selected.Layout, selected.UI) != nil {
		return store.ArchitectureAcceptance{}, model.ErrPolicyDenied
	}
	return observed, nil
}

// OperatorInstructionBinding names the exact artifact a recorded owner
// instruction authorizes the Marshal to accept: an architecture review or,
// when none exists, the pending project contract draft.
type OperatorInstructionBinding struct {
	Kind, Ref, ID string
	Revision      int64
	Digest        string
}

// RecordArchitectureOperatorInstruction is fed only whole user messages from
// the authenticated live Marshal transcript, before any acceptance proposal.
func (r *Runtime) RecordArchitectureOperatorInstruction(ctx context.Context, session, ref, text string) (bool, error) {
	if !architecturecodec.ModelArchitectureApprovalAllowed() || !store.ArchitectureAcceptanceInstruction(text) || r == nil || r.store == nil {
		return false, model.ErrPolicyDenied
	}
	raw, err := readArchitecturePrivateFile(filepath.Join(r.layout.RuntimeDir, "architecture", "review"), "state.json", architectureContextLimit)
	if err != nil {
		return false, err
	}
	selection, err := r.marshalArchitectureSelection(ctx, raw)
	if err != nil {
		return false, err
	}
	return r.store.RecordMarshalArchitectureInstruction(ctx, r.ProjectID(), session, ref, text, selection)
}

func (r *Runtime) acceptArchitectureByMarshal(ctx context.Context, review *architecturereview.Review, session, ref, occurrence, id string, revision int64, digest string) error {
	if !architecturecodec.ModelArchitectureApprovalAllowed() || r == nil || r.store == nil || review == nil {
		return model.ErrPolicyDenied
	}
	return review.AcceptByMarshal(ctx, r.ProjectIdentity(), id, revision, digest, ref, occurrence, func(raw json.RawMessage) error {
		selection, err := r.marshalArchitectureSelection(ctx, raw)
		if err != nil {
			return err
		}
		_, err = r.store.CommitMarshalArchitectureAcceptance(ctx, r.ProjectID(), session, ref, occurrence, selection)
		return err
	})
}

// AcceptArchitectureByMarshal reopens an existing canonical review under its
// writer lease. It needs neither an HTTP listener nor a browser click.
// A live browser owner uses ArchitectureReviewSession.AcceptByMarshal.
func (r *Runtime) AcceptArchitectureByMarshal(ctx context.Context, session, ref, occurrence, id string, revision int64, digest string) error {
	if !architecturecodec.ModelArchitectureApprovalAllowed() || r == nil || r.store == nil {
		return model.ErrPolicyDenied
	}
	lease, err := r.AcquireArchitectureReviewLease()
	if err != nil {
		return err
	}
	defer lease.Close()
	directory := filepath.Join(r.layout.RuntimeDir, "architecture", "review")
	raw, err := readArchitecturePrivateFile(directory, "state.json", architectureContextLimit)
	if err != nil {
		return err
	}
	selected, err := r.marshalArchitectureSelection(ctx, raw)
	if err != nil || selected.ArchitectureID != id || selected.Revision != revision || selected.Digest != digest {
		return model.ErrPolicyDenied
	}
	var state struct {
		Language string `json:"language"`
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return err
	}
	review, err := architecturereview.New(architecturereview.Config{Context: ctx, Project: r.ProjectIdentity(), ArchitectureID: id, StateDir: directory, Language: state.Language})
	if err != nil {
		return err
	}
	return r.acceptArchitectureByMarshal(ctx, review, session, ref, occurrence, id, revision, digest)
}

func (r *Runtime) validateMarshalArchitectureApproval(ctx context.Context, raw json.RawMessage) error {
	var state struct {
		Approved bool `json:"approved"`
		Approval struct {
			Source  string `json:"source"`
			Ref     string `json:"operator_instruction_ref"`
			Receipt string `json:"receipt_id"`
		} `json:"approval"`
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return err
	}
	if !state.Approved || state.Approval.Source != "marshal_operator_command" {
		return nil
	}
	selection, err := r.marshalArchitectureSelection(ctx, raw)
	if err != nil {
		return err
	}
	_, err = r.store.MarshalArchitectureReceipt(ctx, r.ProjectID(), state.Approval.Ref, state.Approval.Receipt, selection)
	return err
}

// AcceptByMarshal is available only on this runtime's live review session.
func (s *ArchitectureReviewSession) AcceptByMarshal(ctx context.Context, session, ref, occurrence, id string, revision int64, digest string) error {
	if s == nil || s.accept == nil {
		return model.ErrPolicyDenied
	}
	select {
	case <-s.Done:
		return model.ErrPolicyDenied
	default:
	}
	return s.accept(ctx, session, ref, occurrence, id, revision, digest)
}
