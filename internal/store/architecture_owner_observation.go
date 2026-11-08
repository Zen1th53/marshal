package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Zen1th53/marshal/internal/architecturecodec"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/statepath"
)

const ownerReviewLimit = 128 << 10

func ownerReviewDirectory(root string, layouts bool) (string, error) {
	path := root
	parts := []string{statepath.DirName, "architecture", "review"}
	if layouts {
		parts = append(parts, "layouts")
	}
	for _, part := range parts {
		path = filepath.Join(path, part)
		info, err := os.Lstat(path)
		if err != nil {
			return "", err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
			return "", model.ErrPolicyDenied
		}
	}
	return path, nil
}

// readOwnerReviewFile confines each private read to a checked directory inode.
func readOwnerReviewFile(directory, name string) ([]byte, error) {
	info, err := os.Lstat(directory)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, model.ErrPolicyDenied
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		return nil, model.ErrPolicyDenied
	}
	before, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Mode().Perm()&0077 != 0 || before.Size() > ownerReviewLimit {
		return nil, model.ErrPolicyDenied
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	actual, err := file.Stat()
	if err != nil || !os.SameFile(before, actual) {
		return nil, model.ErrPolicyDenied
	}
	raw, err := io.ReadAll(io.LimitReader(file, ownerReviewLimit+1))
	if err != nil {
		return nil, err
	}
	after, err := root.Lstat(name)
	if err != nil || !os.SameFile(before, after) || !after.Mode().IsRegular() || after.Mode().Perm()&0077 != 0 || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || len(raw) > ownerReviewLimit {
		return nil, model.ErrPolicyDenied
	}
	return raw, nil
}

// ObserveRegisteredArchitectureOwner derives every binding from the registered
// UID and the live Store connection. A caller cannot supply a root or graph.
func (s *Store) ObserveRegisteredArchitectureOwner(ctx context.Context) (ArchitectureOwnerChallenge, json.RawMessage, error) {
	var c ArchitectureOwnerChallenge
	if s == nil || s.db == nil {
		return c, nil, model.ErrPolicyDenied
	}
	path, err := s.mainDatabasePath(ctx)
	if err != nil {
		return c, nil, err
	}
	if err := s.validateArchitectureFile(ctx, path); err != nil {
		return c, nil, err
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil || real != path {
		return c, nil, model.ErrPolicyDenied
	}
	fileInfo, err := os.Lstat(path)
	if err != nil || !fileInfo.Mode().IsRegular() {
		return c, nil, model.ErrPolicyDenied
	}
	if filepath.Base(path) != "state.db" || filepath.Base(filepath.Dir(path)) != statepath.DirName {
		return c, nil, model.ErrPolicyDenied
	}
	root := filepath.Dir(filepath.Dir(path))
	resolution := projectid.Resolve(ctx, nil, root, filepath.Dir(path))
	if !resolution.Bound || !resolution.AdoptExisting || resolution.NeedsRebind || resolution.ID == "" || resolution.Comparison.Verdict != projectid.VerdictSame {
		return c, nil, model.ErrPolicyDenied
	}
	project, err := s.Project(ctx)
	if err != nil {
		return c, nil, err
	}
	if project.Repository != root && project.Repository != filepath.Join(root, ".git") {
		return c, nil, model.ErrPolicyDenied
	}
	namespace, err := s.ArchitectureRegisteredNamespace(ctx, string(resolution.ID))
	if err != nil || namespace != project.ID {
		return c, nil, model.ErrPolicyDenied
	}
	g, err := s.ArchitectureTargetGenerationFor(ctx, string(resolution.ID))
	if err != nil {
		return c, nil, err
	}
	source, err := SnapshotArchitectureSource(ctx, root)
	if err != nil {
		return c, nil, err
	}
	if g.ProjectID != string(resolution.ID) || g.Root != root || g.SourceRoot != root || g.StorePath != path || g.GitDirectory != source.GitDirectory {
		return c, nil, model.ErrPolicyDenied
	}
	directory, err := ownerReviewDirectory(root, false)
	if err != nil {
		return c, nil, err
	}
	raw, err := readOwnerReviewFile(directory, "state.json")
	if err != nil {
		return c, nil, err
	}
	packet, err := architecturecodec.ValidateStoredState(raw, g.ProjectID)
	if err != nil {
		return c, nil, err
	}
	var selected struct {
		Architecture json.RawMessage `json:"architecture"`
		ID           string          `json:"architecture_id"`
		Revision     int64           `json:"revision"`
		Digest       string          `json:"digest"`
		Layout       string          `json:"layout_digest"`
		UI           string          `json:"ui_digest"`
	}
	if json.Unmarshal(raw, &selected) != nil || len(selected.Architecture) == 0 {
		return c, nil, model.ErrPolicyDenied
	}
	var projection struct {
		Layout string `json:"layout_digest"`
	}
	if json.Unmarshal(packet, &projection) != nil || projection.Layout != selected.Layout {
		return c, nil, model.ErrPolicyDenied
	}
	if len(selected.Layout) != 64 {
		return c, nil, model.ErrPolicyDenied
	}
	for _, ch := range selected.Layout {
		if ch < '0' || ch > '9' && ch < 'a' || ch > 'f' {
			return c, nil, model.ErrPolicyDenied
		}
	}
	layoutDirectory, err := ownerReviewDirectory(root, true)
	if err != nil {
		return c, nil, err
	}
	layout, err := readOwnerReviewFile(layoutDirectory, selected.Layout+".json")
	if err != nil {
		return c, nil, err
	}
	_, _, digest, err := architecturecodec.BindStoredLayouts(selected.Architecture, layout)
	if err != nil || digest != selected.Layout {
		return c, nil, model.ErrPolicyDenied
	}
	c.Generation = g.Generation
	c.SourceDirty = source.Dirty
	c.Binding = ArchitectureAcceptance{SourceDirty: source.Dirty, ArchitectureTarget: g.ArchitectureTarget, ArchitectureID: selected.ID, Revision: selected.Revision, Digest: selected.Digest, LayoutDigest: selected.Layout, UIDigest: selected.UI, SourceHead: source.Head, SourceTree: source.Tree}
	var nonce [32]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return c, nil, err
	}
	c.Nonce = hex.EncodeToString(nonce[:])
	c.ExpiresUnix = time.Now().Add(5 * time.Minute).Unix()
	return c, selected.Architecture, nil
}

// ImportObservedArchitectureReceipt reobserves before the transactional writer.
func (s *Store) ImportObservedArchitectureReceipt(ctx context.Context, receipt SignedArchitectureReceipt) (ArchitectureAcceptance, error) {
	if !architecturecodec.ArchitectureApprovalOriginAllowed(receipt.Challenge.Binding) {
		return ArchitectureAcceptance{}, model.ErrPolicyDenied
	}
	return s.importObservedArchitectureReceipt(ctx, receipt, receipt.Challenge.Binding.Origin)
}

// importObservedArchitectureReceipt reobserves the live canonical review target.
func (s *Store) importObservedArchitectureReceipt(ctx context.Context, receipt SignedArchitectureReceipt, origin string) (ArchitectureAcceptance, error) {
	c, facts, err := s.ObserveRegisteredArchitectureOwner(ctx)
	if err != nil {
		return ArchitectureAcceptance{}, err
	}
	expected := receipt.Challenge.Binding
	if !architecturecodec.ArchitectureApprovalOriginAllowed(expected) || origin != expected.Origin {
		return ArchitectureAcceptance{}, model.ErrPolicyDenied
	}
	c.Binding.Origin = expected.Origin
	c.Binding.ReceiptID = expected.ReceiptID
	c.Binding.ReceiptDigest = expected.ReceiptDigest
	c.Binding.EnvelopeDigest = expected.EnvelopeDigest
	c.Binding.ModelSelfApproval = expected.ModelSelfApproval
	c.Binding.OperatorInstructionRef = expected.OperatorInstructionRef
	if c.Binding != expected || c.Generation != receipt.Challenge.Generation || c.SourceDirty != receipt.Challenge.SourceDirty {
		return ArchitectureAcceptance{}, model.ErrPolicyDenied
	}
	if err := s.validateArchitectureFile(ctx, expected.StorePath); err != nil {
		return ArchitectureAcceptance{}, err
	}
	return s.AcceptArchitectureReceiptWithFacts(ctx, receipt, c, facts)
}

// ViewRegisteredArchitectureOwner projects the same registered review for the
// isolated browser; this has no signing or acceptance authority.
func (s *Store) ViewRegisteredArchitectureOwner(ctx context.Context) (json.RawMessage, error) {
	c, _, err := s.ObserveRegisteredArchitectureOwner(ctx)
	if err != nil {
		return nil, err
	}
	directory, err := ownerReviewDirectory(c.Binding.Root, false)
	if err != nil {
		return nil, err
	}
	raw, err := readOwnerReviewFile(directory, "state.json")
	if err != nil {
		return nil, err
	}
	if _, err = architecturecodec.ValidateStoredState(raw, c.Binding.ProjectID); err != nil {
		return nil, err
	}
	var state map[string]json.RawMessage
	if json.Unmarshal(raw, &state) != nil {
		return nil, model.ErrPolicyDenied
	}
	var selected struct {
		ID       string `json:"architecture_id"`
		Revision int64  `json:"revision"`
		Digest   string `json:"digest"`
		Layout   string `json:"layout_digest"`
		UI       string `json:"ui_digest"`
	}
	if json.Unmarshal(raw, &selected) != nil || c.Binding.MatchesReview(selected.ID, selected.Revision, selected.Digest, selected.Layout, selected.UI) != nil {
		return nil, model.ErrPolicyDenied
	}
	layoutDirectory, err := ownerReviewDirectory(c.Binding.Root, true)
	if err != nil {
		return nil, err
	}
	layout, err := readOwnerReviewFile(layoutDirectory, c.Binding.LayoutDigest+".json")
	if err != nil {
		return nil, err
	}
	_, boundLayouts, layoutDigest, err := architecturecodec.BindStoredLayouts(state["architecture"], layout)
	if err != nil || layoutDigest != c.Binding.LayoutDigest {
		return nil, model.ErrPolicyDenied
	}
	boundRaw, err := json.Marshal(boundLayouts)
	if err != nil {
		return nil, err
	}
	state["layouts"] = boundRaw
	accepted, err := s.currentArchitectureAcceptance(ctx, c.Binding.ProjectID)
	state["approved"] = []byte("false")
	if err == nil && accepted.ArchitectureID == c.Binding.ArchitectureID && accepted.Revision == c.Binding.Revision && accepted.Digest == c.Binding.Digest && accepted.LayoutDigest == c.Binding.LayoutDigest && accepted.UIDigest == c.Binding.UIDigest && accepted.SourceHead == c.Binding.SourceHead && accepted.SourceTree == c.Binding.SourceTree {
		state["approved"] = []byte("true")
	}
	delete(state, "approval")
	return json.Marshal(state)
}
