package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/Zen1th53/marshal/internal/architecturecodec"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/statepath"
)

const marshalArchitectureSchema = `
CREATE TABLE IF NOT EXISTS architecture_marshal_instructions (
 project_id TEXT NOT NULL REFERENCES projects(project_id), instruction_ref TEXT NOT NULL,
 session_id TEXT NOT NULL, instruction TEXT NOT NULL, selection BLOB NOT NULL,
 PRIMARY KEY(project_id,instruction_ref)
);
CREATE TABLE IF NOT EXISTS architecture_marshal_receipts (
 project_id TEXT NOT NULL, instruction_ref TEXT NOT NULL, receipt_id TEXT NOT NULL,
 receipt BLOB NOT NULL, digest TEXT NOT NULL,
 PRIMARY KEY(project_id,receipt_id), UNIQUE(project_id,instruction_ref),
 FOREIGN KEY(project_id,instruction_ref) REFERENCES architecture_marshal_instructions(project_id,instruction_ref)
);
CREATE TRIGGER IF NOT EXISTS architecture_marshal_instructions_no_update BEFORE UPDATE ON architecture_marshal_instructions BEGIN SELECT RAISE(ABORT,'immutable operator instruction'); END;
CREATE TRIGGER IF NOT EXISTS architecture_marshal_instructions_no_delete BEFORE DELETE ON architecture_marshal_instructions BEGIN SELECT RAISE(ABORT,'immutable operator instruction'); END;
CREATE TRIGGER IF NOT EXISTS architecture_marshal_receipts_no_update BEFORE UPDATE ON architecture_marshal_receipts BEGIN SELECT RAISE(ABORT,'immutable architecture receipt'); END;
CREATE TRIGGER IF NOT EXISTS architecture_marshal_receipts_no_delete BEFORE DELETE ON architecture_marshal_receipts BEGIN SELECT RAISE(ABORT,'immutable architecture receipt'); END;
`

// ArchitectureAcceptanceInstruction accepts an explicit whole operator message.
// Quoted requests, negations and repository text cannot accidentally match.
func ArchitectureAcceptanceInstruction(text string) bool {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "accept architecture", "approve architecture", "accept the architecture", "approve the architecture", "/marshal accept-architecture":
		return true
	}
	return false
}

func (s *Store) validateMarshalArchitectureSelection(ctx context.Context, project string, a ArchitectureAcceptance) error {
	if !architecturecodec.ModelArchitectureApprovalAllowed() || s == nil || s.db == nil || a.Policy != "private_extreme" || project == "" || a.ProjectID == "" || a.ArchitectureID == "" || a.Revision < 1 {
		return model.ErrPolicyDenied
	}
	p, err := s.Project(ctx)
	if err != nil || p.ID != project || (p.Repository != a.Root && p.Repository != filepath.Join(a.Root, ".git")) {
		return model.ErrPolicyDenied
	}
	path, err := s.mainDatabasePath(ctx)
	if err != nil || path != a.StorePath || s.validateArchitectureFile(ctx, path) != nil {
		return model.ErrPolicyDenied
	}
	observed, _, err := s.ObserveMarshalArchitecture(ctx)
	if err != nil || observed != a {
		return model.ErrPolicyDenied
	}
	return nil
}

// ObserveMarshalArchitecture derives the native review binding from the live
// database, canonical project identity, private graph, layouts and source.
func (s *Store) ObserveMarshalArchitecture(ctx context.Context) (ArchitectureAcceptance, json.RawMessage, error) {
	deny := func() (ArchitectureAcceptance, json.RawMessage, error) {
		return ArchitectureAcceptance{}, nil, model.ErrPolicyDenied
	}
	if !architecturecodec.ModelArchitectureApprovalAllowed() || s == nil || s.db == nil {
		return deny()
	}
	path, err := s.mainDatabasePath(ctx)
	if err != nil || s.validateArchitectureFile(ctx, path) != nil || filepath.Base(path) != "state.db" || filepath.Base(filepath.Dir(path)) != statepath.DirName {
		return deny()
	}
	root := filepath.Dir(filepath.Dir(path))
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil || resolved != root {
		return deny()
	}
	identity := projectid.Resolve(ctx, nil, root, filepath.Dir(path))
	if !identity.Bound || !identity.AdoptExisting || identity.NeedsRebind || identity.ID == "" || identity.Comparison.Verdict != projectid.VerdictSame {
		return deny()
	}
	p, err := s.Project(ctx)
	if err != nil || (p.Repository != root && p.Repository != filepath.Join(root, ".git")) {
		return deny()
	}
	directory, err := ownerReviewDirectory(root, false)
	if err != nil {
		return deny()
	}
	raw, err := readOwnerReviewFile(directory, "state.json")
	if err != nil {
		return deny()
	}
	if _, err := architecturecodec.ValidateStoredState(raw, string(identity.ID)); err != nil {
		return deny()
	}
	var state struct {
		ID       string          `json:"architecture_id"`
		Revision int64           `json:"revision"`
		Digest   string          `json:"digest"`
		Layout   string          `json:"layout_digest"`
		UI       string          `json:"ui_digest"`
		Graph    json.RawMessage `json:"architecture"`
	}
	if json.Unmarshal(raw, &state) != nil || len(state.Layout) != 64 || strings.ContainsAny(state.Layout, "/\\.") {
		return deny()
	}
	layoutDir, err := ownerReviewDirectory(root, true)
	if err != nil {
		return deny()
	}
	layout, err := readOwnerReviewFile(layoutDir, state.Layout+".json")
	if err != nil {
		return deny()
	}
	_, _, digest, err := architecturecodec.BindStoredLayouts(state.Graph, layout)
	if err != nil || digest != state.Layout {
		return deny()
	}
	source, err := SnapshotArchitectureSource(ctx, root)
	if err != nil {
		return deny()
	}
	a := ArchitectureAcceptance{ArchitectureTarget: ArchitectureTarget{ProjectID: string(identity.ID), Root: root, StorePath: path, SourceRoot: root, Protocol: architecturecodec.Version, Policy: "private_extreme"}, ArchitectureID: state.ID, Revision: state.Revision, Digest: state.Digest, LayoutDigest: state.Layout, UIDigest: state.UI, SourceHead: source.Head, SourceTree: source.Tree, SourceDirty: source.Dirty}
	return a, raw, nil
}

func (s *Store) MarshalArchitectureAcceptanceFor(ctx context.Context, uid string) (ArchitectureAcceptance, error) {
	selection, raw, err := s.ObserveMarshalArchitecture(ctx)
	if err != nil || uid != selection.ProjectID {
		return ArchitectureAcceptance{}, model.ErrPolicyDenied
	}
	var state struct {
		Approved bool `json:"approved"`
		Approval struct {
			Source  string `json:"source"`
			Ref     string `json:"operator_instruction_ref"`
			Receipt string `json:"receipt_id"`
		} `json:"approval"`
	}
	if json.Unmarshal(raw, &state) != nil || !state.Approved || state.Approval.Source != "marshal_operator_command" {
		return ArchitectureAcceptance{}, model.ErrPolicyDenied
	}
	p, err := s.Project(ctx)
	if err != nil {
		return ArchitectureAcceptance{}, err
	}
	return s.MarshalArchitectureReceipt(ctx, p.ID, state.Approval.Ref, state.Approval.Receipt, selection)
}

func (s *Store) marshalArchitectureFactAvailable(ctx context.Context, uid, digest string) bool {
	accepted, err := s.MarshalArchitectureAcceptanceFor(ctx, uid)
	if err != nil {
		return false
	}
	observed, raw, err := s.ObserveMarshalArchitecture(ctx)
	if err != nil || accepted.Validate(observed.ArchitectureTarget, observed.SourceHead, observed.SourceTree) != nil || accepted.SourceDirty != observed.SourceDirty || accepted.MatchesReview(observed.ArchitectureID, observed.Revision, observed.Digest, observed.LayoutDigest, observed.UIDigest) != nil {
		return false
	}
	var state struct {
		Graph json.RawMessage `json:"architecture"`
	}
	if json.Unmarshal(raw, &state) != nil {
		return false
	}
	facts, err := architecturecodec.ArchitectureFactDigests(state.Graph)
	if err != nil {
		return false
	}
	for _, fact := range facts {
		if fact == digest {
			return true
		}
	}
	return false
}

// RecordMarshalArchitectureInstruction is called only by the bound operator
// chat observer. Model proposals cannot call this through an MCP tool.
func (s *Store) RecordMarshalArchitectureInstruction(ctx context.Context, project, session, ref, text string, selection ArchitectureAcceptance) (bool, error) {
	if session == "" || ref == "" || len(ref) > 200 || !ArchitectureAcceptanceInstruction(text) {
		return false, model.ErrPolicyDenied
	}
	if err := s.validateMarshalArchitectureSelection(ctx, project, selection); err != nil {
		return false, err
	}
	if _, err := s.db.ExecContext(ctx, marshalArchitectureSchema); err != nil {
		return false, err
	}
	raw, err := json.Marshal(selection)
	if err != nil {
		return false, err
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO architecture_marshal_instructions VALUES(?,?,?,?,?) ON CONFLICT DO NOTHING`, project, ref, session, text, raw)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

func (s *Store) CommitMarshalArchitectureAcceptance(ctx context.Context, project, session, ref, occurrence string, selection ArchitectureAcceptance) (ArchitectureAcceptance, error) {
	if session == "" || ref == "" || occurrence == "" {
		return ArchitectureAcceptance{}, model.ErrPolicyDenied
	}
	if err := s.validateMarshalArchitectureSelection(ctx, project, selection); err != nil {
		return ArchitectureAcceptance{}, err
	}
	raw, err := json.Marshal(selection)
	if err != nil {
		return ArchitectureAcceptance{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ArchitectureAcceptance{}, err
	}
	defer tx.Rollback()
	var bound []byte
	var text string
	if err := tx.QueryRowContext(ctx, `SELECT selection,instruction FROM architecture_marshal_instructions WHERE project_id=? AND session_id=? AND instruction_ref=?`, project, session, ref).Scan(&bound, &text); err != nil || !bytes.Equal(bound, raw) || !ArchitectureAcceptanceInstruction(text) {
		return ArchitectureAcceptance{}, model.ErrPolicyDenied
	}
	a := selection
	a.Origin = "owner_command"
	a.ModelSelfApproval = true
	a.OperatorInstructionRef = ref
	a.ReceiptID = occurrence
	h := sha256.Sum256(raw)
	a.EnvelopeDigest = hex.EncodeToString(h[:])
	raw, err = json.Marshal(a)
	if err != nil {
		return ArchitectureAcceptance{}, err
	}
	h = sha256.Sum256(raw)
	if _, err = tx.ExecContext(ctx, `INSERT INTO architecture_marshal_receipts VALUES(?,?,?,?,?) ON CONFLICT DO NOTHING`, project, ref, occurrence, raw, hex.EncodeToString(h[:])); err != nil {
		return ArchitectureAcceptance{}, err
	}
	var saved []byte
	if err = tx.QueryRowContext(ctx, `SELECT receipt FROM architecture_marshal_receipts WHERE project_id=? AND receipt_id=? AND instruction_ref=?`, project, occurrence, ref).Scan(&saved); err != nil || !bytes.Equal(saved, raw) {
		return ArchitectureAcceptance{}, model.ErrPolicyDenied
	}
	if err = s.validateArchitectureTransaction(ctx, tx, selection.StorePath); err != nil {
		return ArchitectureAcceptance{}, err
	}
	if err = tx.Commit(); err != nil {
		return ArchitectureAcceptance{}, err
	}
	a.ReceiptDigest = hex.EncodeToString(h[:])
	return a, nil
}

// MarshalArchitectureReceipt authenticates native review metadata against the
// append-only SQLite receipt and its original operator instruction.
func (s *Store) MarshalArchitectureReceipt(ctx context.Context, project, ref, occurrence string, selection ArchitectureAcceptance) (ArchitectureAcceptance, error) {
	if err := s.validateMarshalArchitectureSelection(ctx, project, selection); err != nil {
		return ArchitectureAcceptance{}, err
	}
	var raw, bound []byte
	var digest, text string
	err := s.db.QueryRowContext(ctx, `SELECT r.receipt,r.digest,i.selection,i.instruction FROM architecture_marshal_receipts r JOIN architecture_marshal_instructions i ON i.project_id=r.project_id AND i.instruction_ref=r.instruction_ref WHERE r.project_id=? AND r.instruction_ref=? AND r.receipt_id=?`, project, ref, occurrence).Scan(&raw, &digest, &bound, &text)
	if err != nil {
		return ArchitectureAcceptance{}, model.ErrPolicyDenied
	}
	h := sha256.Sum256(raw)
	expected, err := json.Marshal(selection)
	if err != nil || digest != hex.EncodeToString(h[:]) || !bytes.Equal(bound, expected) || !ArchitectureAcceptanceInstruction(text) {
		return ArchitectureAcceptance{}, model.ErrPolicyDenied
	}
	var a ArchitectureAcceptance
	if json.Unmarshal(raw, &a) != nil || !architecturecodec.ArchitectureApprovalOriginAllowed(a) || a.ReceiptID != occurrence || a.OperatorInstructionRef != ref {
		return ArchitectureAcceptance{}, model.ErrPolicyDenied
	}
	h = sha256.Sum256(expected)
	if a.EnvelopeDigest != hex.EncodeToString(h[:]) {
		return ArchitectureAcceptance{}, model.ErrPolicyDenied
	}
	selected := a
	selected.ReceiptID, selected.ReceiptDigest, selected.EnvelopeDigest, selected.Origin, selected.OperatorInstructionRef = "", "", "", "", ""
	selected.ModelSelfApproval = false
	if selected != selection {
		return ArchitectureAcceptance{}, model.ErrPolicyDenied
	}
	a.ReceiptDigest = digest
	return a, nil
}
