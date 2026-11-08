//go:build !governed

package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/store"
)

type process05ReviewProvenance struct {
	Version         int                          `json:"version"`
	Meaning         string                       `json:"meaning"`
	Project         string                       `json:"project"`
	MarshalRun      string                       `json:"marshal_run"`
	MarshalRevision int64                        `json:"marshal_revision"`
	ExecutionParent string                       `json:"execution_parent,omitempty"`
	ResultCommit    string                       `json:"result_commit"`
	AttemptBasis    string                       `json:"attempt_basis"`
	ModelMeaning    string                       `json:"model_meaning"`
	Plan            process05PlanProvenance      `json:"plan"`
	Transport       process05TransportProvenance `json:"transport"`
	Binding         providerTransportBinding     `json:"binding"`
	Runtime         process05ArtifactProvenance  `json:"runtime"`
}
type process05PlanProvenance struct {
	ID                 string `json:"id"`
	Version            int64  `json:"version"`
	State              string `json:"state"`
	GoalID             string `json:"goal_id"`
	GoalRevision       int64  `json:"goal_revision"`
	PlanDigest         string `json:"plan_digest"`
	MarshalScopeDigest string `json:"marshal_scope_digest"`
	HandoffEvidenceID  string `json:"handoff_evidence_id"`
	HandoffSequence    int64  `json:"handoff_sequence"`
}
type process05TransportProvenance struct {
	ApprovalID            string `json:"approval_id"`
	Status                string `json:"status"`
	ActionDigest          string `json:"action_digest"`
	StateDigest           string `json:"state_digest"`
	NativeState           string `json:"native_state"`
	Occurrence            string `json:"occurrence"`
	Launch                string `json:"launch"`
	Owner                 string `json:"owner"`
	ReviewerProvider      string `json:"reviewer_provider"`
	PayloadDigest         string `json:"payload_digest"`
	ConsumptionEvidenceID string `json:"consumption_evidence_id"`
}
type process05ArtifactProvenance struct {
	ExecutableSHA256 string `json:"executable_sha256,omitempty"`
	VCSRevision      string `json:"vcs_revision,omitempty"`
	VCSModified      *bool  `json:"vcs_modified,omitempty"`
	BuildVersion     string `json:"build_version,omitempty"`
	IdentityBasis    string `json:"identity_basis"`
}

const process05ProvenancePrefix = "runtime provenance process05 "
const process05ProvenanceBound = 8192

// This is a historical record of admitted facts, never a fresh authorization.
// Current output/check acceptance, native model identity and paid call counts
// are not inferred from a consumed approval or an admission ordinal.
func (r *Runtime) marshalProcess05Provenance(ctx context.Context, mr marshal.Run, run *execution.ExecutionRun, task execution.TaskExecution) ([]marshal.CommandRecord, error) {
	// Each refusal names the failed check, so a stopped run says which fact
	// moved instead of a single opaque reason.
	refuse := func(check string) ([]marshal.CommandRecord, error) {
		return nil, fmt.Errorf("process 05: canonical review provenance differs (%s)", check)
	}
	if task.ApprovalID == "" {
		return nil, nil
	} // Legacy/injected execution carries no invented transport proof.
	if r == nil || run == nil || r.Execution() == nil || r.Execution().Engine() == nil {
		return refuse("runtime unavailable")
	}
	engine := r.Execution().Engine()
	a, err := engine.ApprovalManager().GetApproval(task.ApprovalID)
	if err != nil {
		return refuse("transport approval unreadable")
	}
	if a.OperationType != providerTransportOperation {
		return nil, nil
	} // No transport claim for legacy task-only approvals.
	if a.Status != execution.ApprovalConsumed {
		return refuse("transport approval not consumed")
	}
	current, err := engine.GetRun(ctx, run.RunID)
	if err != nil {
		return refuse("execution run unreadable")
	}
	actual, ok := current.Tasks[task.TaskID]
	actualJSON, _ := json.Marshal(actual)
	taskJSON, _ := json.Marshal(task)
	if current.ProjectID != run.ProjectID || current.SessionID != run.SessionID || current.GoalID != run.GoalID || current.GoalRevision != run.GoalRevision || current.PlanID != run.PlanID || current.PlanVersion != run.PlanVersion || current.ParentRunID != run.ParentRunID || current.BaseCommit != run.BaseCommit {
		return refuse("execution run binding moved")
	}
	if !ok || !bytes.Equal(actualJSON, taskJSON) || task.RunID != current.RunID || task.State != execution.TaskCompletedPendingVerify || task.ResultCommit == "" || current.PlanID != mr.PlanID || current.PlanVersion != mr.PlanVersion || string(current.ProjectID) != r.ProjectIdentity() {
		return refuse("task record or plan binding moved")
	}
	if err := engine.ValidateRunGoal(ctx, current); err != nil {
		return refuse("run goal invalid")
	}
	if current.ParentRunID != "" {
		if err := engine.ValidateCompletedTaskLaneBinding(ctx, current.RunID); err != nil {
			return refuse("task lane binding invalid")
		}
	}
	marshalID := marshalRunID(mr)
	saved, err := r.store.GetMarshalRun(ctx, r.ProjectID(), marshalID)
	if err != nil {
		return refuse("Marshal run unreadable")
	}
	index := taskIndex(saved.Value, task.TaskID)
	if !mr.Process05Bound || !saved.Value.Process05Bound || saved.Value.PlanID != mr.PlanID || saved.Value.PlanVersion != mr.PlanVersion || index < 0 {
		return refuse("Marshal run plan binding moved")
	}
	mt := saved.Value.Tasks[index]
	if mt.Title != task.Description || !sameStrings(mt.Files, task.TargetFiles) {
		return refuse("Marshal task title or files differ")
	}
	if mt.Mode != marshal.Governed || normalizeProvider(mt.Worker) != task.AssignedHarness || (mt.Process05WaitRunID != "" && mt.Process05WaitRunID != current.RunID) || (mt.Process05WaitApprovalID != "" && mt.Process05WaitApprovalID != a.ApprovalID) {
		return refuse("Marshal task mode, worker or wait binding differs")
	}
	// Historical: the review's launch need not still be live. An approved-plan
	// runtime proof is accepted only for its reserved decider and chain.
	proof, err := r.transportReviewProof(ctx, a.ApprovalID, false)
	if err != nil || proof.Request.RunID != marshalID {
		return refuse("applied Marshal transport review absent")
	}
	var reviewed execution.RuntimeApproval
	if strictProvenanceJSON(proof.Request.ApprovalBinding, &reviewed) != nil {
		return refuse("reviewed approval binding malformed")
	}
	marker := "exact binding "
	at := strings.LastIndex(reviewed.Scope, marker)
	if at < 0 {
		return refuse("reviewed scope lacks exact binding")
	}
	var b providerTransportBinding
	if strictProvenanceJSON(reviewed.Scope[at+len(marker):], &b) != nil {
		return refuse("exact binding malformed")
	}
	req := transportApprovalRequest(b)
	action := execution.ComputeActionDigest(req.OperationType, req.TargetResource, req.DiffPreview, req.Parameters)
	state := execution.ComputeStateDigest(req.CurrentState)
	if a.ExpiresAt == nil || reviewed.ExpiresAt == nil || !a.ExpiresAt.Equal(*reviewed.ExpiresAt) || !a.CreatedAt.Equal(reviewed.CreatedAt) {
		return refuse("approval expiry or creation differs from review")
	}
	if b.Version != 1 || !validProviderTransportEndpoints(b) || a.ApprovalID != reviewed.ApprovalID || a.OperationType != reviewed.OperationType || a.ActionDigest != action || a.StateDigest != state || reviewed.ActionDigest != action || reviewed.StateDigest != state || a.Scope != req.Scope || reviewed.Scope != req.Scope || a.TargetResource != req.TargetResource || reviewed.TargetResource != req.TargetResource || a.RiskLevel != model.R3 || reviewed.RiskLevel != model.R3 || a.RunID != b.Run || a.TaskID != b.Task || a.PlanID != b.Plan || a.PlanVersion != b.PlanVersion || reviewed.RunID != b.Run || reviewed.TaskID != b.Task || reviewed.PlanID != b.Plan || reviewed.PlanVersion != b.PlanVersion {
		return refuse("approval digests or scope differ from binding")
	}
	canonicalTask, err := r.store.GetTask(ctx, task.CanonicalTaskID)
	if err != nil || b.Risk != string(canonicalTask.Risk) {
		return refuse("canonical task risk differs")
	}
	// A role assignment records no explicit principal. Compare the consumed
	// transport's actual principal with the same canonical resolver used at
	// admission, while preserving the independent explicit-assignment field.
	var resolved string
	switch b.Provider {
	case "codex":
		resolved, err = r.process05CodexAgent(ctx, task)
	case "claude":
		resolved, err = r.process05ClaudeAgent(ctx, task)
	case "opencode":
		resolved, err = r.process05OpenCodeAgent(ctx, task)
	case "agy":
		resolved, err = r.process05AntigravityAgent(ctx, task)
	}
	if err != nil || resolved == "" || resolved != b.Agent {
		return refuse("resolved worker principal differs")
	}
	if b.Project != r.ProjectIdentity() || b.Run != current.RunID || b.Task != task.TaskID || b.CanonicalTask != task.CanonicalTaskID || b.Plan != current.PlanID || b.PlanVersion != current.PlanVersion || b.Base != task.BaseCommit || b.Worktree != task.WorktreePath || b.Provider != task.AssignedHarness || b.Model != task.AssignedModel || b.AssignedAgent != task.AssignedAgent || b.AgentRole != task.AssignedRole || b.Attempt != task.Attempts {
		return refuse("binding identity differs from task")
	}
	planDigest, err := r.transportPlanDigest(ctx, current, task)
	if err != nil || planDigest != b.PlanDigest {
		return refuse("plan digest differs")
	}
	p, err := r.Plans().Current(ctx, projectid.ID(r.ProjectIdentity()))
	if err != nil {
		return refuse("current plan unreadable")
	}
	goal, err := r.store.GetGoalContract(ctx, p.Goal.GoalID, p.Goal.Revision)
	if err != nil {
		return refuse("goal unreadable")
	}
	if _, err := plan.PrepareHandoff(p, goal, p.ProjectID, p.UpdatedAt); err != nil {
		return refuse("plan handoff invalid")
	}
	// Reconstruct pre-execution expected outputs from the approved plan. Native
	// stdout/artifact labels appended by a worker never redefine that package.
	pre := task
	pre.OutputArtifacts = nil
	pre.ApprovalRequired = true
	for _, pt := range p.Tasks {
		if pt.ID == task.TaskID && pt.ExpectedOutput != "" {
			pre.OutputArtifacts = []string{pt.ExpectedOutput}
		}
	}
	pkg, err := execution.BuildConstraintPackage(goal, pre, p)
	if err != nil || transportPackageDigest(pkg) != b.Package {
		return refuse("constraint package digest differs")
	}
	profile, err := r.providerTransportHarnessProfile(ctx, b.Provider)
	if err != nil || profile == nil {
		return refuse("harness profile unreadable")
	}
	raw, _ := json.Marshal(profile)
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != b.Profile {
		return refuse("harness profile digest differs")
	}
	historical, err := r.store.Process05HistoricalEvidence(ctx, store.Process05HistoricalEvidenceRequest{Project: r.ProjectID(), Plan: p.ID, PlanVersion: p.Version, Goal: goal.ID, GoalRevision: goal.Revision, ConstraintDigest: p.Goal.ConstraintDigest, Approval: a.ApprovalID, Run: b.Run, Task: b.Task, Launch: proof.Source.Launch, Occurrence: proof.Source.ID, Object: "provider-transport " + b.Provider + " " + req.TargetResource})
	if err != nil {
		return refuse("historical consumption evidence unavailable")
	}
	if a.ExpiresAt == nil || historical.ConsumedAt.Before(a.CreatedAt) || !historical.ConsumedAt.Before(*a.ExpiresAt) {
		return refuse("consumption outside approval window")
	}
	if b.Original.Required {
		original, err := engine.ApprovalManager().GetApproval(b.Original.ID)
		expectedAction := execution.ComputeActionDigest("TASK_EXECUTE", filepath.Join(task.TargetFiles...), "", task.Description)
		expectedState := execution.ComputeStateDigest("")
		if err != nil || original.Status != execution.ApprovalConsumed || original.OperationType != "TASK_EXECUTE" || original.Scope != "task_mutation" || original.TargetResource != filepath.Join(task.TargetFiles...) || original.RiskLevel != model.R2 || original.RunID != b.Run || original.TaskID != b.Task || original.PlanID != b.Plan || original.PlanVersion != b.PlanVersion || original.ActionDigest != b.Original.Action || original.ActionDigest != expectedAction || original.StateDigest != b.Original.State || original.StateDigest != expectedState || original.ExpiresAt == nil || !original.ExpiresAt.Equal(b.Original.Expires) || !historical.ConsumedAt.Before(*original.ExpiresAt) {
			return refuse("original task approval binding differs")
		}
	} else if b.Original.ID != "" || b.Original.Action != "" || b.Original.State != "" || !b.Original.Expires.IsZero() {
		return refuse("unexpected original approval binding")
	}
	required, err := r.transportTaskApprovalRequired(ctx, current, task)
	if err != nil || (required && !b.Original.Required) {
		return refuse("task approval requirement differs")
	}
	output := process05ReviewProvenance{Version: 1, Meaning: "historical admitted facts; not a live grant, paid-call count, result acceptance or source-review verdict", Project: r.ProjectIdentity(), MarshalRun: marshalID, MarshalRevision: saved.Revision, ExecutionParent: current.ParentRunID, ResultCommit: task.ResultCommit, AttemptBasis: "execution_engine_admission", ModelMeaning: "binding.Model is the requested CLI selector; emitted native identity is ProviderObservation.Model/HandIn.Model, absence is UNKNOWN", Binding: cloneProviderTransportBinding(b), Plan: process05PlanProvenance{ID: p.ID, Version: p.Version, State: string(p.State), GoalID: goal.ID, GoalRevision: goal.Revision, PlanDigest: b.PlanDigest, MarshalScopeDigest: saved.Value.ApprovalScopeDigest, HandoffEvidenceID: historical.HandoffID, HandoffSequence: historical.HandoffSequence}, Transport: process05TransportProvenance{ApprovalID: a.ApprovalID, Status: string(a.Status), ActionDigest: a.ActionDigest, StateDigest: a.StateDigest, NativeState: "APPLIED", Occurrence: proof.Source.ID, Launch: proof.Source.Launch, Owner: proof.Source.Owner, ReviewerProvider: proof.Source.Provider, PayloadDigest: proof.Source.Digest, ConsumptionEvidenceID: historical.ConsumptionID}, Runtime: currentProcess05Artifact()}
	encoded, err := json.Marshal(output)
	if err != nil || len(encoded) > process05ProvenanceBound {
		return nil, fmt.Errorf("process 05: review provenance exceeds bound")
	}
	return []marshal.CommandRecord{{Command: process05ProvenancePrefix + current.RunID, Output: string(encoded)}}, nil
}
func strictProvenanceJSON(raw string, out any) error {
	if raw == "" || len(raw) > 16384 {
		return errors.New("provenance JSON bound")
	}
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return errors.New("provenance trailing data")
	}
	canonical, err := json.Marshal(out)
	if err != nil || !bytes.Equal(canonical, []byte(raw)) {
		return errors.New("provenance is not canonical JSON")
	}
	return nil
}

var process05ArtifactOnce sync.Once
var process05Artifact process05ArtifactProvenance

func currentProcess05Artifact() process05ArtifactProvenance {
	process05ArtifactOnce.Do(func() {
		process05Artifact.IdentityBasis = "loaded executable and Go VCS build metadata; independent source review not implied"
		// /proc/self/exe pins the running inode even when an installation path is replaced.
		f, err := os.Open("/proc/self/exe")
		if err == nil {
			defer f.Close()
			h := sha256.New()
			if _, err := io.Copy(h, f); err == nil {
				process05Artifact.ExecutableSHA256 = hex.EncodeToString(h.Sum(nil))
			}
		}
		if info, ok := debug.ReadBuildInfo(); ok {
			process05Artifact.BuildVersion = info.Main.Version
			for _, setting := range info.Settings {
				switch setting.Key {
				case "vcs.revision":
					process05Artifact.VCSRevision = setting.Value
				case "vcs.modified":
					v := setting.Value == "true"
					if setting.Value == "true" || setting.Value == "false" {
						process05Artifact.VCSModified = &v
					}
				}
			}
		}
	})
	out := process05Artifact
	if out.VCSModified != nil {
		value := *out.VCSModified
		out.VCSModified = &value
	}
	return out
}
