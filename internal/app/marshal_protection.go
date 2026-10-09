package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/events"
	"github.com/Zen1th53/marshal/internal/marshal"
)

func (s *MarshalService) constitutionService() *ConstitutionService {
	return &ConstitutionService{runtime: &Runtime{store: s.Store}, registry: constitution.Default(), now: s.clock}
}

func (s *MarshalService) executionAdmission(ctx context.Context, runID, taskID string, run marshal.Run) error {
	service := s.constitutionService()
	_, found, err := service.SessionVersion(ctx, runID)
	if err != nil {
		return err
	}
	if !found {
		if _, err := service.BindSession(ctx, runID, s.ProjectID, constitution.ModeStandard); err != nil {
			return err
		}
	}
	envelope, state, err := s.gateInputs(ctx, runID, taskID, run, constitution.DomainProjectMutation)
	if err != nil {
		return err
	}
	envelope.Action = "marshal execution admission"
	if taskID == "" && run.ApprovalScopeDigest == "" {
		// Resume also inspects drafts and explains pre-approval pauses. This
		// snapshot is decision evidence, not plan or worker authorization.
		data, err := json.Marshal(run)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		envelope.StateDigest = "sha256:" + hex.EncodeToString(sum[:])
		state.AuthorizedActor = true
	}
	result, err := service.Decide(ctx, DecideRequest{Envelope: envelope, State: state})
	if err != nil {
		return err
	}
	if !result.Permitted() {
		return fmt.Errorf("constitutional admission refused: %s (%s)", result.Verdict.Reason, result.Response)
	}
	return s.checkGoverningIntegrity(ctx, runID, run)
}

// ReservedMergePurpose binds one popup decision to the accepted commit and run revision.
func ReservedMergePurpose(taskID, result string, revision int64) string {
	return fmt.Sprintf("reserved:%s:%s:%d", taskID, result, revision)
}

func (s *MarshalService) approveReservedMerge(ctx context.Context, runID, taskID string, run marshal.Run, revision int64) error {
	t := run.Tasks[taskIndex(run, taskID)]
	if t.Mode != marshal.Governed {
		return nil
	}
	// Inspect the actual commit, including both sides of renames, rather than claims.
	changed, err := gitMarshal(ctx, s.Repository, "diff", "--name-only", "--no-renames", "-z", t.BaseCommit, t.ResultCommit)
	if err != nil {
		return err
	}
	var reserved []string
	for _, name := range strings.Split(changed, "\x00") {
		if name != "" && marshal.ReservedPath(name) {
			reserved = append(reserved, name)
		}
	}
	if len(reserved) == 0 {
		return nil
	}
	if s.ApprovalActor != nil {
		if actor, err := s.ApprovalActor(ctx, runID, ReservedMergePurpose(taskID, t.ResultCommit, revision)); err == nil && actor != "" {
			return nil
		}
	}
	if s.ReservedMergeRequest != nil {
		s.ReservedMergeRequest(ctx, runID, taskID, revision, t.ResultCommit, reserved)
	}
	return errors.New("reserved governance changes require explicit operator popup approval")
}

// Operational records under .marshal change during normal work. Only governing
// configuration and instructions there belong in the integrity baseline.
func governingFile(name string) bool {
	if !marshal.ReservedPath(name) {
		return false
	}
	if strings.HasPrefix(name, ".marshal/") {
		lower := strings.ToLower(name)
		return strings.Contains(lower, "policy") || strings.Contains(lower, "constitution") || strings.Contains(lower, "config") || strings.HasSuffix(lower, "instructions.md") || strings.HasSuffix(lower, "agents.md") || strings.HasSuffix(lower, "claude.md")
	}
	return true
}

func governingDigest(root string) (string, error) {
	var names []string
	err := filepath.WalkDir(root, func(full string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, full)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "node_modules" || rel == ".marshal/worktrees" || rel == ".marshal/artifacts" || rel == ".marshal/logs" || rel == ".marshal/proposals" || rel == ".marshal/inbox" {
				return filepath.SkipDir
			}
			return nil
		}
		if governingFile(rel) {
			names = append(names, rel)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(names)
	h := sha256.New()
	for _, name := range names {
		info, err := os.Lstat(filepath.Join(root, name))
		if err != nil {
			return "", err
		}
		var data []byte
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(filepath.Join(root, name))
			if err != nil {
				return "", err
			}
			resolved, err := os.Stat(filepath.Join(root, name))
			if err != nil {
				return "", err
			}
			if !resolved.Mode().IsRegular() {
				return "", fmt.Errorf("governing symlink target is not a regular file: %s", name)
			}
			content, err := os.ReadFile(filepath.Join(root, name))
			if err != nil {
				return "", err
			}
			data = append([]byte(target+"\x00"), content...)
		} else {
			if !info.Mode().IsRegular() {
				return "", fmt.Errorf("governing path is not a regular file: %s", name)
			}
			data, err = os.ReadFile(filepath.Join(root, name))
			if err != nil {
				return "", err
			}
		}
		sum := sha256.Sum256(data)
		fmt.Fprintf(h, "%s\x00%s\x00%x\x00", name, info.Mode().String(), sum)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

func (s *MarshalService) checkGoverningIntegrity(ctx context.Context, runID string, run marshal.Run) error {
	if run.GoverningDigest == "" {
		return nil
	} // Runs created before the baseline existed.
	digest, err := governingDigest(s.Repository)
	if err == nil && digest == run.GoverningDigest {
		return nil
	}
	if recordErr := s.record(ctx, runID, "", events.EventTypeMarshalEscalated, map[string]any{"reason": "governing files changed unexpectedly; inspect policy, CI and instruction files before continuing", "expected_digest": run.GoverningDigest, "observed_digest": digest}); recordErr != nil {
		return recordErr
	}
	return errors.New("governing files changed unexpectedly; inspect policy, CI and instruction files before continuing")
}

func (s *MarshalService) recordConstitutionalVerdict(ctx context.Context, envelope constitution.Envelope, verdict constitution.Verdict) error {
	violations := constitution.ViolationsFrom(verdict, envelope, s.clock())
	result := DecideResult{Verdict: verdict, Violations: violations}
	if response, ok := constitution.MostSevereResponse(violations); ok {
		result.Response = response
	}
	return s.constitutionService().record(ctx, envelope, result)
}

// CheckGoverningIntegrity lets the active runtime monitor report tampering even
// while no worker is being dispatched. Closed and unapproved runs have no active baseline.
func (s *MarshalService) CheckGoverningIntegrity(ctx context.Context, runID string) error {
	run, _, err := s.load(ctx, runID)
	if err != nil {
		return err
	}
	if run.State == marshal.Closed || run.State == marshal.Drafting {
		return nil
	}
	return s.checkGoverningIntegrity(ctx, runID, run)
}
