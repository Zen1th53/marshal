package tui

// Cheap advanced-menu bindings (tasks/tui-v3/TUI3-0014..0020, "cheap" rows).
//
// Every entry here reuses a path that already exists elsewhere — a slash
// command, a CLI subcommand, or another binding — and adds only the typed
// form, the target readback and the durable reread that proves the result.
// Nothing here is a new execution path: each executor names the canonical
// method it forwards to, and every mutation still goes through the shared
// confirmation flow (Cancel first, explicit Proceed).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/goalintake"
	"github.com/Zen1th53/marshal/internal/learning"
	"github.com/Zen1th53/marshal/internal/legal"
	"github.com/Zen1th53/marshal/internal/memory/importer"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/optimization"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/statepath"
)

// MenuActionAuthority is optional, like MemoryActionAuthority: an authority
// that does not implement it leaves these entries unavailable with a reason
// instead of acquiring the operations by accident.
type MenuActionAuthority interface {
	// MenuProjectRoot is the open project's root on disk.
	MenuProjectRoot() string
	// MenuTeamSession rereads the collaboration session of this workspace.
	MenuTeamSession(ctx context.Context) (*model.TeamSession, error)
	// MenuCollaborate is the /msg and /handoff path (handleCollaboration).
	MenuCollaborate(ctx context.Context, operation, target, content string, expectedTurn int64, idempotencyKey string) (app.CollaborationResult, error)
	// MenuRemember is Runtime.CommandRemember with an explicit memory kind.
	MenuRemember(ctx context.Context, kind model.MemoryKind, title, body string) (model.MemoryRecordV2, error)
	// MenuMemoryRecord rereads one project memory record.
	MenuMemoryRecord(ctx context.Context, id string) (model.MemoryRecordV2, error)
	// MenuTombstone is `marshal memory tombstone` (store.TombstoneMemory + IndexRecord).
	MenuTombstone(ctx context.Context, id string, expectedRevision int64, reason string) (model.MemoryRecordV2, error)
	// MenuLearningCommit / MenuLearningInvalidate are `marshal learning commit|invalidate`.
	MenuLearningCommit(ctx context.Context, in app.CommitInput) (learning.Commit, error)
	MenuLearningInvalidate(ctx context.Context, commitID, verificationID, provenance string, changed []learning.Dependency) (learning.Commit, error)
	MenuLearningGet(ctx context.Context, id string) (learning.Commit, error)
	// MenuStartOptimization is `marshal optimization start`.
	MenuStartOptimization(ctx context.Context, in app.StartCycleInput) (optimization.Cycle, error)
	MenuOptimizationGet(ctx context.Context, id string) (optimization.Cycle, error)
	// MenuFormGoal is `marshal goal explain`: goalintake.Form, nothing persisted.
	MenuFormGoal(ctx context.Context, request string) (goalintake.Intake, error)
	// MenuLegalExport is `marshal legal export --output PATH`.
	MenuLegalExport(ctx context.Context, output string) (*legal.ExportResult, error)
	// MenuAntigravityHistory reads the agy conversation databases the native
	// capture would import; MenuImportAntigravity runs that capture once.
	MenuAntigravityHistory(ctx context.Context) (antigravityHistory, error)
	MenuImportAntigravity(ctx context.Context) ([]model.MemoryRecordV2, error)
}

// cheapMenuBindings returns the entries this file owns. Bindings merges it
// last, so these replace the Requires-only placeholders of the same ids.
func cheapMenuBindings(s *ControlSource) map[ActionID]Binding {
	out := map[ActionID]Binding{}
	for _, setup := range []struct{ id, title string }{
		{"CTUI-0201", "Create local state"},
		{"CTUI-0202", "Install default capability policy"},
		{"CTUI-0203", "Install pack/runtime contracts"},
	} {
		out[ActionID(setup.id)] = initReadbackBinding(s, ActionID(setup.id), setup.title)
	}
	out["CTUI-0229"] = Binding{Action: "CTUI-0229", Title: "Form goal from request", Safety: SafetyPlain,
		Inputs:         []InputSpec{{Key: "request", Label: "Request (your words)", Required: true}},
		PrepareRequest: s.prepareFormGoal, Execute: s.executeFormGoal}
	out["CTUI-0317"] = Binding{Action: "CTUI-0317", Title: "Messages", Safety: SafetyGoverned,
		Inputs: []InputSpec{
			{Key: "to", Label: "Recipient agent ID (or all)", Required: true, Default: "all"},
			{Key: "content", Label: "Message", Required: true},
		},
		Prepare:        s.collaborationPrecheck,
		PrepareRequest: s.prepareCollaboration("message"), Execute: s.executeCollaboration("message")}
	out["CTUI-0319"] = Binding{Action: "CTUI-0319", Title: "Ownership handoff", Safety: SafetyGoverned,
		Inputs: []InputSpec{
			{Key: "to", Label: "Target role", Required: true},
			{Key: "content", Label: "Handoff reason", Required: true},
		},
		Prepare:        s.collaborationPrecheck,
		PrepareRequest: s.prepareCollaboration("handoff"), Execute: s.executeCollaboration("handoff")}
	for id, spec := range map[ActionID]struct {
		title string
		kind  model.MemoryKind
	}{
		"CTUI-0427": {"Capture episode", model.MemoryKindEpisodic},
		"CTUI-0428": {"Capture environment experience", model.MemoryKindProcedural},
	} {
		kind := spec.kind
		out[id] = Binding{Action: id, Title: spec.title, Safety: SafetyPlain,
			Inputs:         []InputSpec{{Key: "title", Label: "Memory title", Required: true}, {Key: "body", Label: string(kind) + " memory", Required: true}},
			PrepareRequest: s.prepareMemoryLedger,
			Execute: func(ctx context.Context, req ActionRequest) (Outcome, error) {
				return s.executeRememberKind(ctx, req, kind)
			}}
	}
	out["CTUI-0456"] = Binding{Action: "CTUI-0456", Title: "Invalidate, supersede, or tombstone", Safety: SafetyDestructive,
		Inputs: []InputSpec{
			{Key: "memory_id", Label: "Exact memory ID to tombstone", Required: true},
			{Key: "reason", Label: "Reason", Required: true},
		},
		PrepareRequest: s.prepareTombstone, Execute: s.executeTombstone}
	out["CTUI-0461"] = Binding{Action: "CTUI-0461", Title: "Commit verified outcome", Safety: SafetyGoverned,
		Inputs:         []InputSpec{{Key: "path", Label: "Project-relative commit input JSON (as `marshal learning commit`)", Required: true}},
		PrepareRequest: s.prepareMenuJSON("learning-commit"), Execute: s.executeLearningCommit}
	out["CTUI-0465"] = Binding{Action: "CTUI-0465", Title: "Invalidate learned item", Safety: SafetyGoverned,
		Inputs:         []InputSpec{{Key: "path", Label: "Project-relative invalidation JSON (as `marshal learning invalidate`)", Required: true}},
		PrepareRequest: s.prepareMenuJSON("learning-invalidate"), Execute: s.executeLearningInvalidate}
	out["CTUI-0573"] = Binding{Action: "CTUI-0573", Title: "Start from Process 07 commit", Safety: SafetyGoverned,
		Inputs:         []InputSpec{{Key: "path", Label: "Project-relative cycle input JSON (as `marshal optimization start`)", Required: true}},
		PrepareRequest: s.prepareMenuJSON("optimization-start"), Execute: s.executeStartOptimization}
	out["CTUI-0487"] = Binding{Action: "CTUI-0487", Title: "Import Antigravity SQLite", Safety: SafetyPlain,
		Prepare: s.prepareAntigravityImport, Execute: s.executeAntigravityImport}
	out["CTUI-0787"] = Binding{Action: "CTUI-0787", Title: "Legal evidence-pack export", Safety: SafetyPlain,
		Inputs:         []InputSpec{{Key: "output", Label: "New project-relative output file (.tar.gz)", Required: true, Default: statepath.DirName + "/exports/legal-evidence-pack.tar.gz", Validate: validateProjectRelative}},
		PrepareRequest: s.prepareLegalExport, Execute: s.executeLegalExport}
	for id, b := range out {
		if _, ok := s.Authority.(MenuActionAuthority); !ok {
			b.Requires = "authenticated local runtime authority is unavailable"
			if strings.HasPrefix(b.Title, "Create local state") || strings.HasPrefix(b.Title, "Install ") {
				b.Requires = "initialization runs through `marshal init`; reading it back needs the authenticated local runtime authority"
			}
			b.Execute = nil
			out[id] = b
		}
	}

	// CTUI-0480 is /memory import's own binding with a format field.
	out["CTUI-0480"] = memoryAssimilateBinding(s)
	// CTUI-0249: invalidating a claim is recorded through Process 06's
	// governed challenge (Runtime.CommandVerification "challenge"), which binds
	// the claim to canonical counter-evidence and blocks the verification
	// until it is resolved. That is the existing verification path; the
	// binding is the CTUI-0352 one under this spec id.
	if challenge, ok := verificationActionBindings(s)["CTUI-0352"]; ok {
		challenge.Action, challenge.Title = "CTUI-0249", "Invalidate affected claims"
		out["CTUI-0249"] = challenge
	}
	return out
}

func menuAuthority(s *ControlSource) (MenuActionAuthority, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	a, ok := s.Authority.(MenuActionAuthority)
	if !ok {
		return nil, ErrNoBinding
	}
	return a, nil
}

// --- CTUI-0201..0203: initialization readbacks ---

// initReadbackBinding reads back what `marshal init` (app.Bootstrap) created.
// A running workspace is always initialized, so the readback resolves to
// BLOCKED "already initialized" with the digest of the files it found; when
// something is missing it names it and the command that repairs it.
func initReadbackBinding(s *ControlSource, id ActionID, title string) Binding {
	files := map[ActionID][]string{
		"CTUI-0201": {statepath.DirName},
		"CTUI-0202": {"CAPABILITIES.yaml"},
		"CTUI-0203": {"PACK-VERSION.yaml", "RUNTIME-VERSION.yaml"},
	}[id]
	return Binding{Action: id, Title: title, Safety: SafetyPlain,
		Prepare: func(ctx context.Context) (Target, error) {
			a, err := menuAuthority(s)
			if err != nil {
				return Target{}, err
			}
			root := a.MenuProjectRoot()
			var present, missing []string
			h := sha256.New()
			for _, name := range files {
				path := filepath.Join(root, name)
				info, err := os.Stat(path)
				if err != nil {
					missing = append(missing, name)
					continue
				}
				present = append(present, name)
				if info.Mode().IsRegular() {
					if data, err := os.ReadFile(path); err == nil {
						h.Write(data)
					}
				}
			}
			if len(missing) > 0 {
				return Target{}, fmt.Errorf("not initialized here: %s missing; run `marshal init` (or `marshal doctor` to repair) outside the TUI", strings.Join(missing, ", "))
			}
			return Target{}, fmt.Errorf("already initialized: %s present #%s; initialization runs once through `marshal init`",
				strings.Join(present, ", "), shortDigest(hex.EncodeToString(h.Sum(nil))))
		},
		// Prepare always resolves to a verdict; a submit can only arrive when
		// state changed underneath, and then initialization is still not a
		// TUI operation.
		Execute: func(_ context.Context, req ActionRequest) (Outcome, error) {
			reason := "initialization runs through `marshal init`, not from an open workspace"
			return Outcome{Verdict: VerdictBlocked, Detail: reason, Target: req.Target, Proof: Blocked(reason, "marshal init", string(id))}, nil
		}}
}

// --- CTUI-0229: form goal from request (pure) ---

func (s *ControlSource) prepareFormGoal(ctx context.Context, req ActionRequest) (Target, error) {
	if _, err := menuAuthority(s); err != nil {
		return Target{}, err
	}
	request := strings.TrimSpace(req.Inputs["request"])
	if request == "" {
		return Target{}, errors.New("describe what you want")
	}
	digest, err := digestTarget(struct{ Request, Project string }{request, s.ProjectID})
	if err != nil {
		return Target{}, err
	}
	return Target{Kind: "goal-request", ID: s.SessionID, Digest: digest,
		Scope:   "read only: goalintake.Form; nothing is persisted",
		Summary: fmt.Sprintf("form a goal candidate from %d characters of request text", len(request))}, nil
}

func (s *ControlSource) executeFormGoal(ctx context.Context, req ActionRequest) (Outcome, error) {
	a, err := menuAuthority(s)
	if err != nil {
		return Outcome{}, err
	}
	intake, err := a.MenuFormGoal(ctx, strings.TrimSpace(req.Inputs["request"]))
	if err != nil {
		return refusal("the goal could not be formed", err, req.Target, "internal/goalintake/formation.go")
	}
	parts := []string{fmt.Sprintf("Understood as: %s", intake.Interpretation)}
	if n := len(intake.Constraints); n > 0 {
		texts := make([]string, 0, n)
		for _, c := range intake.Constraints {
			texts = append(texts, c.Text)
		}
		parts = append(parts, "limits: "+strings.Join(texts, "; "))
	}
	if elevated := intake.Assessment.Elevated(); len(elevated) > 0 {
		parts = append(parts, "worth noting: "+strings.Join(elevated, ", "))
	}
	if len(intake.Ambiguities) > 0 {
		parts = append(parts, "needs: "+intake.Ambiguities[0].Question)
	}
	parts = append(parts, fmt.Sprintf("confirmation %s; not persisted (confirm it with Persist and activate confirmed goal)", intake.Confirmation))
	detail := strings.Join(parts, " | ")
	return Outcome{Verdict: VerdictPass, Detail: detail,
		Target:   Target{Kind: "goal-candidate", ID: intake.RequestDigest, Digest: intake.RequestDigest, Scope: req.Target.Scope, Summary: intake.Interpretation},
		Evidence: intake.RequestDigest,
		Proof:    Known(detail, "internal/goalintake/formation.go")}, nil
}

// --- CTUI-0317 / CTUI-0319: collaboration ---

// collaborationPrecheck is the pre-form check of Messages and Ownership
// handoff: with no team session the form would only be refused after it was
// filled in (e2e 2026-11-06), so it is refused before it opens.
func (s *ControlSource) collaborationPrecheck(ctx context.Context) (Target, error) {
	a, err := menuAuthority(s)
	if err != nil {
		return Target{}, err
	}
	if session, err := a.MenuTeamSession(ctx); err != nil || session == nil {
		return Target{}, fmt.Errorf("no collaboration session %s exists for this workspace; start a team session first", s.SessionID)
	}
	return Target{}, nil
}

func (s *ControlSource) prepareCollaboration(operation string) func(context.Context, ActionRequest) (Target, error) {
	return func(ctx context.Context, req ActionRequest) (Target, error) {
		a, err := menuAuthority(s)
		if err != nil {
			return Target{}, err
		}
		session, err := a.MenuTeamSession(ctx)
		if err != nil || session == nil {
			return Target{}, fmt.Errorf("no collaboration session %s exists for this workspace; nothing was sent. Start a team session first", s.SessionID)
		}
		digest, err := digestTarget(struct {
			Session string
			Turn    int64
			Active  string
			Status  string
			Op, To  string
		}{session.SessionID, session.TurnSequence, session.ActiveTurn, session.Status, operation, req.Inputs["to"]})
		if err != nil {
			return Target{}, err
		}
		summary := fmt.Sprintf("message to %s in session %s at turn %d", req.Inputs["to"], session.SessionID, session.TurnSequence)
		if operation == "handoff" {
			summary = fmt.Sprintf("hand the turn from %s to role %s at turn %d", session.ActiveTurn, req.Inputs["to"], session.TurnSequence)
		}
		return Target{Kind: "team-session", ID: session.SessionID, Revision: session.TurnSequence, Digest: digest,
			Scope: fmt.Sprintf("collaboration session %s", session.SessionID), Summary: summary}, nil
	}
}

func (s *ControlSource) executeCollaboration(operation string) Executor {
	return func(ctx context.Context, req ActionRequest) (Outcome, error) {
		a, err := menuAuthority(s)
		if err != nil {
			return Outcome{}, err
		}
		key := sha256.Sum256([]byte(req.IdempotencyKey))
		result, err := a.MenuCollaborate(ctx, operation, req.Inputs["to"], req.Inputs["content"], req.Target.Revision, hex.EncodeToString(key[:]))
		if errors.Is(err, model.ErrConflict) {
			return refusal("nothing was sent: the session moved on or is not active", err, req.Target, "Runtime.CommandCollaboration")
		}
		if err != nil {
			return refusal("the collaboration command was refused", err, req.Target, "Runtime.CommandCollaboration")
		}
		proof, err := a.MenuTeamSession(ctx)
		if err != nil || proof == nil {
			return unproven(req.Target, req.Target.ID, fmt.Errorf("reread session: %v", err))
		}
		if proof.ActiveTurn != result.ActiveTurn || proof.TurnSequence < req.Target.Revision {
			reason := fmt.Sprintf("session %s did not reread as the command reported (turn %s/%d)", proof.SessionID, proof.ActiveTurn, proof.TurnSequence)
			return Outcome{Verdict: VerdictUnknown, Detail: reason, Target: req.Target, Proof: Unknown(reason, "internal/store/collaboration.go")}, nil
		}
		detail := fmt.Sprintf("Message %s sent to %s.", result.MessageID, req.Inputs["to"])
		if operation == "handoff" {
			detail = fmt.Sprintf("Turn handed to %s (%s); session turn %d.", result.ActiveTurn, req.Inputs["to"], result.TurnSequence)
		}
		return Outcome{Verdict: VerdictPass, Detail: detail,
			Target:   Target{Kind: "team-session", ID: proof.SessionID, Revision: proof.TurnSequence, Scope: req.Target.Scope, Summary: detail},
			Evidence: result.MessageID, Proof: Known(detail, "Runtime.CommandCollaboration")}, nil
	}
}

// --- CTUI-0427 / CTUI-0428: remember with a kind (as CTUI-0424) ---

func (s *ControlSource) executeRememberKind(ctx context.Context, req ActionRequest, kind model.MemoryKind) (Outcome, error) {
	a, err := menuAuthority(s)
	if err != nil {
		return Outcome{}, err
	}
	record, err := a.MenuRemember(ctx, kind, req.Inputs["title"], req.Inputs["body"])
	if err != nil {
		return refusal("the memory could not be recorded", err, req.Target, "internal/app/memory_runtime.go")
	}
	proof, err := a.MenuMemoryRecord(ctx, record.ID)
	if err != nil {
		return unproven(req.Target, record.ID, err)
	}
	if proof.ID != record.ID || proof.Kind != kind || proof.Title != strings.TrimSpace(req.Inputs["title"]) || proof.Body != strings.TrimSpace(req.Inputs["body"]) {
		reason := fmt.Sprintf("memory %s was accepted but its durable reread does not match the typed %s record", record.ID, kind)
		return Outcome{Verdict: VerdictUnknown, Detail: reason, Target: req.Target, Proof: Unknown(reason, "internal/store/memory_v2.go")}, nil
	}
	return Outcome{Verdict: VerdictPass, Detail: fmt.Sprintf("%s memory %s recorded", kind, proof.ID),
		Target: Target{Kind: "memory", ID: proof.ID, Revision: proof.Revision, Digest: proof.ContentDigest, Scope: proof.ScopeID, Summary: proof.Title},
		Proof:  Known(proof.ID, "internal/app/memory_runtime.go")}, nil
}

// --- CTUI-0456: tombstone (as `marshal memory tombstone`) ---

func (s *ControlSource) prepareTombstone(ctx context.Context, req ActionRequest) (Target, error) {
	a, err := menuAuthority(s)
	if err != nil {
		return Target{}, err
	}
	rec, err := a.MenuMemoryRecord(ctx, strings.TrimSpace(req.Inputs["memory_id"]))
	if err != nil {
		return Target{}, fmt.Errorf("read memory %s: %w", req.Inputs["memory_id"], err)
	}
	if rec.Lifecycle == model.MemoryTombstoned {
		return Target{}, fmt.Errorf("memory %s is already tombstoned at revision %d", rec.ID, rec.Revision)
	}
	return Target{Kind: "memory", ID: rec.ID, Revision: rec.Revision, Digest: rec.ContentDigest,
		Scope:   fmt.Sprintf("%s:%s", rec.Scope, rec.ScopeID),
		Summary: fmt.Sprintf("tombstone %s %q (%s @%d)", rec.ID, rec.Title, rec.Lifecycle, rec.Revision)}, nil
}

func (s *ControlSource) executeTombstone(ctx context.Context, req ActionRequest) (Outcome, error) {
	a, err := menuAuthority(s)
	if err != nil {
		return Outcome{}, err
	}
	after, err := a.MenuTombstone(ctx, req.Target.ID, req.Target.Revision, "operator tombstone via TUI: "+strings.TrimSpace(req.Inputs["reason"]))
	if err != nil {
		return refusal("the memory could not be tombstoned", err, req.Target, "internal/store/memory.go")
	}
	proof, err := a.MenuMemoryRecord(ctx, after.ID)
	if err != nil {
		return unproven(req.Target, after.ID, err)
	}
	if proof.Lifecycle != model.MemoryTombstoned || proof.Revision <= req.Target.Revision {
		reason := fmt.Sprintf("memory %s rereads as %s @%d, not tombstoned", proof.ID, proof.Lifecycle, proof.Revision)
		return Outcome{Verdict: VerdictUnknown, Detail: reason, Target: req.Target, Proof: Unknown(reason, "internal/store/memory.go")}, nil
	}
	detail := fmt.Sprintf("tombstoned memory %s at revision %d", proof.ID, proof.Revision)
	return memorySuccess(Target{Kind: "memory", ID: proof.ID, Revision: proof.Revision, Digest: proof.ContentDigest, Scope: req.Target.Scope, Summary: detail},
		detail, proof.ContentDigest, "store.TombstoneMemory"), nil
}

// --- CTUI-0461 / CTUI-0465 / CTUI-0573: JSON-input commands ---

const menuJSONMaxBytes = 1 << 20

// readProjectFile reads one regular file beneath root through os.Root,
// refusing absolute paths, escapes, symlink components, .git and MARSHAL's
// internal state outside its exports/imports folders — the same confinement
// the memory import path uses.
// fileReadReason drops the syscall name a *fs.PathError carries ("statat zz:
// no such file or directory"), which the menu would otherwise show verbatim,
// and says a missing file is missing from the project.
func fileReadReason(err error) error {
	var pe *fs.PathError
	if !errors.As(err, &pe) {
		return err
	}
	if errors.Is(pe.Err, fs.ErrNotExist) {
		return fmt.Errorf("%s does not exist in the project: %w", pe.Path, fs.ErrNotExist)
	}
	return fmt.Errorf("%s: %w", pe.Path, pe.Err)
}

func readProjectFile(root, path string) ([]byte, string, error) {
	rel, err := projectRelative(path)
	if err != nil {
		return nil, "", err
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, "", err
	}
	defer r.Close()
	prefix := ""
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		prefix = filepath.Join(prefix, part)
		info, err := r.Lstat(prefix)
		if err != nil {
			return nil, "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, "", fmt.Errorf("%s: symbolic links are refused", prefix)
		}
	}
	f, err := r.Open(rel)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, "", err
	}
	if !info.Mode().IsRegular() || info.Size() > menuJSONMaxBytes {
		return nil, "", fmt.Errorf("%s is not a regular file of at most %d bytes", rel, menuJSONMaxBytes)
	}
	data, err := io.ReadAll(io.LimitReader(f, menuJSONMaxBytes+1))
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(data)
	return data, hex.EncodeToString(sum[:]), nil
}

func projectRelative(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" || filepath.IsAbs(path) || strings.Contains(path, "\\") {
		return "", errors.New("enter a project-relative path")
	}
	rel := filepath.Clean(path)
	sep := string(filepath.Separator)
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+sep) || rel == ".git" || strings.HasPrefix(rel, ".git"+sep) {
		return "", errors.New("the path must stay inside the project and outside .git")
	}
	for _, dir := range []string{statepath.DirName, ".marshal"} {
		if (rel == dir || strings.HasPrefix(rel, dir+sep)) && !strings.HasPrefix(rel, dir+sep+"exports"+sep) && !strings.HasPrefix(rel, dir+sep+"imports"+sep) {
			return "", fmt.Errorf("MARSHAL state under %s is only reachable through %s/exports or %s/imports", dir, dir, dir)
		}
	}
	return rel, nil
}

func validateProjectRelative(v string) error { _, err := projectRelative(v); return err }

// invalidateInput is `marshal learning invalidate`'s input file shape.
type menuInvalidateInput struct {
	ID           string                `json:"memory_commit_id"`
	Verification string                `json:"verification_id"`
	Provenance   string                `json:"provenance"`
	Changed      []learning.Dependency `json:"changed_dependencies"`
}

func decodeMenuJSON(kind string, data []byte) (any, string, error) {
	switch kind {
	case "learning-commit":
		var in app.CommitInput
		if err := json.Unmarshal(data, &in); err != nil {
			return nil, "", fmt.Errorf("decode learning commit input: %w", err)
		}
		return in, fmt.Sprintf("memory commit %s bound to verification %s: %d candidate(s), %d revision(s), %d invalidation(s)",
			in.ID, in.Verification, len(in.Candidates), len(in.Revisions), len(in.Invalidate)), nil
	case "learning-invalidate":
		var in menuInvalidateInput
		if err := json.Unmarshal(data, &in); err != nil {
			return nil, "", fmt.Errorf("decode invalidation input: %w", err)
		}
		return in, fmt.Sprintf("memory commit %s bound to verification %s: %d changed dependenc(ies)", in.ID, in.Verification, len(in.Changed)), nil
	case "optimization-start":
		var in app.StartCycleInput
		if err := json.Unmarshal(data, &in); err != nil {
			return nil, "", fmt.Errorf("decode optimization input: %w", err)
		}
		// Strip operator-supplied Governance to prevent forged governance metadata;
		// governance must come from verified bound records, not operator-authored JSON.
		in.Governance = optimization.Governance{}
		return in, fmt.Sprintf("optimization %s from memory commit %s: %d candidate(s), %d objective(s)",
			in.ID, in.MemoryCommitID, len(in.Candidates), len(in.Objectives)), nil
	}
	return nil, "", ErrNoBinding
}

func (s *ControlSource) prepareMenuJSON(kind string) func(context.Context, ActionRequest) (Target, error) {
	return func(ctx context.Context, req ActionRequest) (Target, error) {
		a, err := menuAuthority(s)
		if err != nil {
			return Target{}, err
		}
		data, digest, err := readProjectFile(a.MenuProjectRoot(), req.Inputs["path"])
		if err != nil {
			return Target{}, fmt.Errorf("read %s: %w", req.Inputs["path"], fileReadReason(err))
		}
		_, summary, err := decodeMenuJSON(kind, data)
		if err != nil {
			return Target{}, err
		}
		return Target{Kind: kind, ID: filepath.Clean(strings.TrimSpace(req.Inputs["path"])), Digest: digest,
			Scope: "project " + s.ProjectID, Summary: summary}, nil
	}
}

// menuJSONInput rereads the reviewed file and refuses when it changed.
func (s *ControlSource) menuJSONInput(a MenuActionAuthority, kind string, req ActionRequest) (any, error) {
	data, digest, err := readProjectFile(a.MenuProjectRoot(), req.Target.ID)
	if err != nil {
		return nil, err
	}
	if digest != req.Target.Digest {
		return nil, ErrStaleTarget
	}
	in, _, err := decodeMenuJSON(kind, data)
	return in, err
}

func (s *ControlSource) executeLearningCommit(ctx context.Context, req ActionRequest) (Outcome, error) {
	a, err := menuAuthority(s)
	if err != nil {
		return Outcome{}, err
	}
	in, err := s.menuJSONInput(a, "learning-commit", req)
	if err != nil {
		return refusal("the commit input changed or is unreadable", err, req.Target, "LearningService.Commit")
	}
	got, err := a.MenuLearningCommit(ctx, in.(app.CommitInput))
	if err != nil {
		return refusal("the learning commit was refused", err, req.Target, "LearningService.Commit")
	}
	return s.proveLearningCommit(ctx, a, req, got, fmt.Sprintf("memory-commit %s: promoted %d, revised %d, invalidated %d, blocked %d",
		got.ID, len(got.Additions), len(got.Revisions), len(got.Invalidations), len(got.BlockedLearning)))
}

func (s *ControlSource) executeLearningInvalidate(ctx context.Context, req ActionRequest) (Outcome, error) {
	a, err := menuAuthority(s)
	if err != nil {
		return Outcome{}, err
	}
	raw, err := s.menuJSONInput(a, "learning-invalidate", req)
	if err != nil {
		return refusal("the invalidation input changed or is unreadable", err, req.Target, "LearningService.Invalidate")
	}
	in := raw.(menuInvalidateInput)
	got, err := a.MenuLearningInvalidate(ctx, in.ID, in.Verification, in.Provenance, in.Changed)
	if err != nil {
		return refusal("the invalidation was refused", err, req.Target, "LearningService.Invalidate")
	}
	return s.proveLearningCommit(ctx, a, req, got, fmt.Sprintf("memory-commit %s: %d learned item(s) staled", got.ID, len(got.Revisions)))
}

func (s *ControlSource) proveLearningCommit(ctx context.Context, a MenuActionAuthority, req ActionRequest, got learning.Commit, detail string) (Outcome, error) {
	proof, err := a.MenuLearningGet(ctx, got.ID)
	if err != nil {
		return unproven(req.Target, got.ID, err)
	}
	if proof.ID != got.ID || proof.Digest != got.Digest {
		reason := fmt.Sprintf("memory commit %s did not reread with the digest the service returned", got.ID)
		return Outcome{Verdict: VerdictUnknown, Detail: reason, Target: req.Target, Proof: Unknown(reason, "internal/store/learning.go")}, nil
	}
	for _, blocked := range got.BlockedLearning {
		detail += "; blocked: " + blocked
	}
	return memorySuccess(Target{Kind: "memory-commit", ID: proof.ID, Revision: proof.Version, Digest: proof.Digest, Scope: req.Target.Scope, Summary: detail},
		detail, proof.Digest, "LearningService"), nil
}

func (s *ControlSource) executeStartOptimization(ctx context.Context, req ActionRequest) (Outcome, error) {
	a, err := menuAuthority(s)
	if err != nil {
		return Outcome{}, err
	}
	in, err := s.menuJSONInput(a, "optimization-start", req)
	if err != nil {
		return refusal("the cycle input changed or is unreadable", err, req.Target, "OptimizationService.StartCycle")
	}
	got, err := a.MenuStartOptimization(ctx, in.(app.StartCycleInput))
	if err != nil {
		return refusal("the optimization cycle was refused", err, req.Target, "OptimizationService.StartCycle")
	}
	proof, err := a.MenuOptimizationGet(ctx, got.ID)
	if err != nil {
		return unproven(req.Target, got.ID, err)
	}
	if proof.ID != got.ID || proof.Version != got.Version {
		reason := fmt.Sprintf("optimization %s did not reread at version %d", got.ID, got.Version)
		return Outcome{Verdict: VerdictUnknown, Detail: reason, Target: req.Target, Proof: Unknown(reason, "internal/store/optimization.go")}, nil
	}
	detail := fmt.Sprintf("optimization %s version %d: %d candidate(s), %d veto(es)", proof.ID, proof.Version, len(proof.Candidates), len(proof.Vetoes))
	return memorySuccess(Target{Kind: "optimization", ID: proof.ID, Revision: proof.Version, Scope: req.Target.Scope, Summary: detail},
		detail, proof.ID, "OptimizationService.StartCycle"), nil
}

// --- CTUI-0480: assimilate existing project knowledge (/memory import) ---

var memoryAssimilateFormats = []string{"marshal-json", string(importer.FormatCodexJSONL), string(importer.FormatClaudeJSONL), "portable"}

func memoryAssimilateBinding(s *ControlSource) Binding {
	b := Binding{Action: "CTUI-0480", Title: "Assimilate existing project knowledge", Safety: SafetyPlain,
		Inputs: []InputSpec{
			{Key: "path", Label: "Project-relative knowledge file", Required: true},
			{Key: "format", Label: "Format (" + strings.Join(memoryAssimilateFormats, ", ") + ")", Required: true, Default: "marshal-json", Validate: func(v string) error {
				for _, f := range memoryAssimilateFormats {
					if v == f {
						return nil
					}
				}
				return fmt.Errorf("enter one of %s", strings.Join(memoryAssimilateFormats, ", "))
			}},
			{Key: "dry_run", Label: "Dry run (true/false)", Default: "true", Required: true, Validate: validateMemoryBool},
		},
		PrepareRequest: func(ctx context.Context, req ActionRequest) (Target, error) {
			return prepareMemoryFileImport(ctx, s, req, req.Inputs["format"])
		},
		Execute: func(ctx context.Context, req ActionRequest) (Outcome, error) {
			return executeMemoryFileImport(ctx, s, req, req.Inputs["format"])
		}}
	if _, ok := s.Authority.(MemoryActionAuthority); !ok {
		b.Requires = "authenticated local memory action authority is unavailable"
		b.Execute = nil
	}
	return b
}

// --- CTUI-0487: import Antigravity SQLite (the native capture path) ---

type antigravityHistory struct {
	Dir       string
	Databases int
	Digest    string
}

func (s *ControlSource) prepareAntigravityImport(ctx context.Context) (Target, error) {
	a, err := menuAuthority(s)
	if err != nil {
		return Target{}, err
	}
	history, err := a.MenuAntigravityHistory(ctx)
	if err != nil {
		return Target{}, err
	}
	return Target{Kind: "antigravity-history", ID: s.ProjectID, Digest: history.Digest,
		Scope:   "conversations agy recorded for this project, imported as untrusted memory candidates",
		Summary: fmt.Sprintf("%d Antigravity conversation database(s) in %s", history.Databases, knownProjectPath(history.Dir, "").Text)}, nil
}

func (s *ControlSource) executeAntigravityImport(ctx context.Context, req ActionRequest) (Outcome, error) {
	a, err := menuAuthority(s)
	if err != nil {
		return Outcome{}, err
	}
	records, err := a.MenuImportAntigravity(ctx)
	if err != nil {
		return refusal("the Antigravity history could not be imported", err, req.Target, "internal/nativehistory/antigravity.go")
	}
	for _, rec := range records {
		if _, err := a.MenuMemoryRecord(ctx, rec.ID); err != nil {
			return unproven(req.Target, rec.ID, err)
		}
	}
	detail := fmt.Sprintf("%d memory candidate record(s) proposed from Antigravity history; already captured conversations are skipped", len(records))
	return memorySuccess(req.Target, detail, req.Target.Digest, "Runtime.ProposeContinuation"), nil
}

// --- CTUI-0787: legal evidence-pack export ---

func (s *ControlSource) prepareLegalExport(ctx context.Context, req ActionRequest) (Target, error) {
	a, err := menuAuthority(s)
	if err != nil {
		return Target{}, err
	}
	rel, err := projectRelative(req.Inputs["output"])
	if err != nil {
		return Target{}, err
	}
	if _, err := os.Lstat(filepath.Join(a.MenuProjectRoot(), rel)); err == nil {
		return Target{}, fmt.Errorf("%s already exists; choose a new output file", rel)
	}
	digest, err := digestTarget(struct{ Output, Project string }{rel, s.ProjectID})
	if err != nil {
		return Target{}, err
	}
	return Target{Kind: "legal-evidence-pack", ID: rel, Digest: digest,
		Scope:   "legal.ExportPack over this repository's HEAD",
		Summary: "write the legal evidence pack to " + rel}, nil
}

func (s *ControlSource) executeLegalExport(ctx context.Context, req ActionRequest) (Outcome, error) {
	a, err := menuAuthority(s)
	if err != nil {
		return Outcome{}, err
	}
	out := filepath.Join(a.MenuProjectRoot(), req.Target.ID)
	if err := os.MkdirAll(filepath.Dir(out), 0o700); err != nil {
		return refusal("the output folder could not be created", err, req.Target, "internal/legal/export.go")
	}
	result, err := a.MenuLegalExport(ctx, out)
	if err != nil {
		return refusal("the legal evidence pack could not be exported", err, req.Target, "internal/legal/export.go")
	}
	data, err := os.ReadFile(out)
	if err != nil {
		return unproven(req.Target, req.Target.ID, err)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != result.ArchiveSHA256 {
		reason := fmt.Sprintf("%s rereads as #%s, not the exported #%s", req.Target.ID, shortDigest(got), shortDigest(result.ArchiveSHA256))
		return Outcome{Verdict: VerdictUnknown, Detail: reason, Target: req.Target, Proof: Unknown(reason, "internal/legal/export.go")}, nil
	}
	detail := fmt.Sprintf("legal evidence pack %s (%d bytes #%s) for HEAD %s; audit %s", req.Target.ID, len(data), shortDigest(result.ArchiveSHA256), shortDigest(result.SourceHEAD), result.OverallStatus)
	return memorySuccess(req.Target, detail, result.ArchiveSHA256, "legal.ExportPack"), nil
}

// --- runtime authority ---

func (a *runtimeControlAuthority) MenuProjectRoot() string {
	if a == nil || a.runtime == nil {
		return ""
	}
	return a.runtime.ProjectRoot()
}

func (a *runtimeControlAuthority) MenuTeamSession(ctx context.Context) (*model.TeamSession, error) {
	if a == nil || a.runtime == nil {
		return nil, errNoRuntime
	}
	return a.runtime.Store().GetTeamSession(ctx, a.sessionID)
}

// MenuCollaborate is the call handleCollaboration makes for /msg and /handoff.
func (a *runtimeControlAuthority) MenuCollaborate(ctx context.Context, operation, target, content string, expectedTurn int64, idempotencyKey string) (app.CollaborationResult, error) {
	if a == nil || a.runtime == nil || a.localControl == nil {
		return app.CollaborationResult{}, errors.New("authenticated runtime authorization is required")
	}
	input := app.CollaborationCommand{Operation: operation, Content: RedactContent(content, nil)}
	target = strings.TrimSpace(target)
	if operation == "message" {
		if !strings.EqualFold(target, "all") {
			input.To = target
		}
	} else {
		input.TargetRole = model.Role(strings.ToLower(target))
	}
	e := app.CommandEnvelope{ProjectID: a.runtime.ProjectIdentity(), SessionID: a.sessionID, TargetID: a.sessionID,
		ExpectedVersion: expectedTurn, IdempotencyKey: idempotencyKey}
	return a.runtime.CommandCollaboration(a.localControl.Context(ctx), e, input)
}

func (a *runtimeControlAuthority) MenuRemember(ctx context.Context, kind model.MemoryKind, title, body string) (model.MemoryRecordV2, error) {
	if a == nil || a.runtime == nil || a.localControl == nil {
		return model.MemoryRecordV2{}, errors.New("authenticated local operator required")
	}
	if _, err := a.memory(); err != nil {
		return model.MemoryRecordV2{}, err
	}
	return a.runtime.CommandRemember(a.localControl.Context(ctx), app.RememberRequest{
		ProjectID: a.runtime.ProjectID(), ScopeID: a.runtime.ProjectID(),
		Title: title, Body: body, Kind: kind,
	})
}

func (a *runtimeControlAuthority) MenuMemoryRecord(ctx context.Context, id string) (model.MemoryRecordV2, error) {
	if _, err := a.memory(); err != nil {
		return model.MemoryRecordV2{}, err
	}
	return a.runtime.Store().GetMemoryV2(ctx, a.runtime.ProjectID(), id)
}

func (a *runtimeControlAuthority) MenuTombstone(ctx context.Context, id string, expectedRevision int64, reason string) (model.MemoryRecordV2, error) {
	if a == nil || a.localControl == nil {
		return model.MemoryRecordV2{}, errors.New("authenticated local operator required")
	}
	service, err := a.memory()
	if err != nil {
		return model.MemoryRecordV2{}, err
	}
	tombstoned, err := a.runtime.Store().TombstoneMemory(ctx, a.runtime.ProjectID(), id, expectedRevision, reason)
	if err != nil {
		return model.MemoryRecordV2{}, err
	}
	return tombstoned, service.IndexRecord(ctx, tombstoned)
}

func (a *runtimeControlAuthority) menuOperator() error {
	if a == nil || a.runtime == nil || a.localControl == nil {
		return errors.New("authenticated local operator required")
	}
	return a.localControlErr
}

func (a *runtimeControlAuthority) MenuLearningCommit(ctx context.Context, in app.CommitInput) (learning.Commit, error) {
	if err := a.menuOperator(); err != nil {
		return learning.Commit{}, err
	}
	return a.runtime.Learning().Commit(ctx, in)
}

func (a *runtimeControlAuthority) MenuLearningInvalidate(ctx context.Context, commitID, verificationID, provenance string, changed []learning.Dependency) (learning.Commit, error) {
	if err := a.menuOperator(); err != nil {
		return learning.Commit{}, err
	}
	return a.runtime.Learning().Invalidate(ctx, commitID, verificationID, provenance, changed)
}

func (a *runtimeControlAuthority) MenuLearningGet(ctx context.Context, id string) (learning.Commit, error) {
	if a == nil || a.runtime == nil {
		return learning.Commit{}, errNoRuntime
	}
	return a.runtime.Learning().Get(ctx, id)
}

func (a *runtimeControlAuthority) MenuStartOptimization(ctx context.Context, in app.StartCycleInput) (optimization.Cycle, error) {
	if err := a.menuOperator(); err != nil {
		return optimization.Cycle{}, err
	}
	return a.runtime.Optimization().StartCycle(ctx, in)
}

func (a *runtimeControlAuthority) MenuOptimizationGet(ctx context.Context, id string) (optimization.Cycle, error) {
	if a == nil || a.runtime == nil {
		return optimization.Cycle{}, errNoRuntime
	}
	return a.runtime.Optimization().Get(ctx, id)
}

// MenuFormGoal mirrors `marshal goal explain`: the same formation request,
// with the workspace's own session instead of the CLI's default one.
func (a *runtimeControlAuthority) MenuFormGoal(ctx context.Context, request string) (goalintake.Intake, error) {
	if a == nil || a.runtime == nil {
		return goalintake.Intake{}, errNoRuntime
	}
	root := a.runtime.ProjectRoot()
	qualification := projectid.QualifyGit(ctx, nil, root)
	scope, scopeErr := projectid.ResolveScope(root, "", qualification)
	return goalintake.Form(goalintake.FormationRequest{
		Request: request, ProjectID: a.runtime.AdmittedProjectIdentity(), SessionID: a.sessionID,
		Version: constitution.Current,
		Context: goalintake.RequestContext{
			Recoverable:   qualification.State.Recoverable(),
			DirtyWorktree: qualification.Dirty,
			ScopeKnown:    scopeErr == nil && scope.Root != "",
		},
	})
}

func (a *runtimeControlAuthority) MenuLegalExport(ctx context.Context, output string) (*legal.ExportResult, error) {
	if err := a.menuOperator(); err != nil {
		return nil, err
	}
	return legal.ExportPack(ctx, a.runtime.ProjectRoot(), output)
}

// menuAntigravityWatch is the watch the native agy launch builds
// (newAntigravityHistoryWatch + guardHistoryWatch), with the same index, so
// a conversation already captured by a live session is not imported twice.
func (a *runtimeControlAuthority) menuAntigravityWatch() (*nativeHistoryWatch, error) {
	if err := a.menuOperator(); err != nil {
		return nil, err
	}
	root := a.runtime.ProjectRoot()
	watch, err := newAntigravityHistoryWatch(root)
	if err != nil {
		return nil, err
	}
	runtime := a.runtime
	watch.authorized = func(path string) bool {
		_, err := os.Stat(watch.antigravitySummaries)
		return runtime.HasReadGrant(path) && (os.IsNotExist(err) || runtime.HasReadGrant(watch.antigravitySummaries))
	}
	watch.captureTools = true
	watch.indexPath = filepath.Join(root, statepath.DirName, "antigravity", "history-index.json")
	return watch, nil
}

func (a *runtimeControlAuthority) MenuAntigravityHistory(ctx context.Context) (antigravityHistory, error) {
	watch, err := a.menuAntigravityWatch()
	if err != nil {
		return antigravityHistory{}, err
	}
	entries, err := os.ReadDir(watch.dir)
	if err != nil {
		return antigravityHistory{}, fmt.Errorf("no Antigravity history to import: %s is unreadable (%v); run agy in this project first", knownProjectPath(watch.dir, "").Text, err)
	}
	var rows []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".db") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		rows = append(rows, fmt.Sprintf("%s:%d:%d", entry.Name(), info.Size(), info.ModTime().UnixNano()))
	}
	if len(rows) == 0 {
		return antigravityHistory{}, fmt.Errorf("no Antigravity conversation databases in %s; run agy in this project first", knownProjectPath(watch.dir, "").Text)
	}
	if !watch.authorized(watch.dir) {
		return antigravityHistory{}, fmt.Errorf("MARSHAL has no read grant for %s; grant it with /permission before importing", knownProjectPath(watch.dir, "").Text)
	}
	sort.Strings(rows)
	digest, err := digestTarget(rows)
	return antigravityHistory{Dir: watch.dir, Databases: len(rows), Digest: digest}, err
}

func (a *runtimeControlAuthority) MenuImportAntigravity(ctx context.Context) ([]model.MemoryRecordV2, error) {
	watch, err := a.menuAntigravityWatch()
	if err != nil {
		return nil, err
	}
	if err := watch.loadIndex(); err != nil {
		return nil, err
	}
	var proposed []model.MemoryRecordV2
	watch.consume = func(tr importer.SessionTranscript) error {
		records, _, err := a.runtime.ProposeContinuation(context.WithoutCancel(ctx), tr)
		proposed = append(proposed, records...)
		return err
	}
	if err := watch.sync(); err != nil {
		return proposed, err
	}
	return proposed, watch.saveIndexIfChanged()
}

var _ MenuActionAuthority = (*runtimeControlAuthority)(nil)
