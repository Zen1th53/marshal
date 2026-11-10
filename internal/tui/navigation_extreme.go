//go:build !governed

package tui

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
)

var buildResolvedGapIDs = []ActionID{"CTUI-0043", "CTUI-0044", "CTUI-0045", "CTUI-0048", "CTUI-0051", "CTUI-0052", "CTUI-0071", "CTUI-0073"}

// Models must not swallow another section's gap and its canonical neighbours.
func buildModelsGap(node *Node) bool { return sectionTitleOf(node) == "Models" }

// Use the same availability boundary for activation and its rendered affordance.
// A legacy refusal callback being bound is not permission to open its dialog.
func (v *NavView) buildActionDispatchRefusal(action *Node) string {
	if availability := v.buildActionAvailability(action); !availability.Enabled {
		return availability.Reason
	}
	return ""
}

// A static manifest destination is not evidence that a mutation callback is
// attached. Show the actual adapter's reason before the user activates it.
func (v *NavView) buildActionAvailability(action *Node) ActionAvailability {
	availability := Availability(action)
	if !availability.Enabled {
		return availability
	}
	if v.control.source == nil {
		availability.Enabled, availability.Reason = false, "no execution authority is attached to this workspace"
		return availability
	}
	if action.SpecID == "CTUI-0050" || action.SpecID == "CTUI-0051" || action.SpecID == "CTUI-0052" {
		if _, supported := v.control.source.Authority.(approvedCellAuthority); !supported {
			availability.Enabled, availability.Reason = false, "the attached authority does not expose canonical approved-check cell control"
			return availability
		}
	}
	if action.SpecID == "CTUI-0071" {
		if _, supported := v.control.source.Authority.(memoryRestoreAuthority); !supported {
			availability.Enabled, availability.Reason = false, "the attached authority does not expose canonical selected-record memory restore"
			return availability
		}
	}
	if buildTaskControlOperations[ActionID(action.SpecID)] != "" {
		if _, supported := v.control.source.Authority.(taskControlAuthority); !supported {
			availability.Enabled, availability.Reason = false, "the attached authority does not expose canonical task control"
			return availability
		}
	}
	switch action.SpecID {
	case "CTUI-0077", "CTUI-0078", "CTUI-0081", "CTUI-0082":
		if _, supported := v.control.source.Authority.(goalBudgetAuthority); !supported {
			availability.Enabled, availability.Reason = false, fmt.Sprintf(budgetContractOwner, action.SpecID)
			return availability
		}
	}
	if action.SpecID == "CTUI-0079" || action.SpecID == "CTUI-0080" {
		if _, supported := v.control.source.Authority.(goalBudgetControlAuthority); !supported {
			availability.Enabled, availability.Reason = false, fmt.Sprintf(budgetContractOwner, action.SpecID)
			return availability
		}
	}
	binding, found := v.control.source.Bindings()[ActionID(action.SpecID)]
	if found && binding.Bound() {
		return availability
	}
	reason := binding.Gap
	if reason == "" {
		reason = binding.Requires
	}
	if reason == "" {
		reason = "no canonical action binding exists in this build"
	}
	availability.Enabled, availability.Reason = false, communityLabel(reason)
	return availability
}

var buildTaskControlOperations = map[ActionID]string{
	"CTUI-0043": "pause", "CTUI-0044": "resume", "CTUI-0045": "retry",
	"CTUI-0046": "cancel",
}

var buildUnboundProviderGaps = map[string][]string{
	"CTUI-0144": {"internal/adapter/adapter.go", "internal/harness/intelligence.go", "internal/store/harness.go", "internal/execution/rate_limit.go"},
	"CTUI-0148": {"internal/adapter/adapter.go", "internal/harness/intelligence.go", "internal/store/harness.go", "internal/execution/rate_limit.go"},
	"CTUI-0149": {"internal/adapter/adapter.go", "internal/harness/intelligence.go", "internal/store/harness.go", "internal/execution/rate_limit.go"},
	"CTUI-0151": {"internal/adapter/adapter.go", "internal/harness/intelligence.go", "internal/store/harness.go", "internal/execution/rate_limit.go"},
	"CTUI-0157": {"internal/cloud/config.go", "internal/cloud/state.go", "internal/cloud/session.go", "internal/cloud/gate.go", "internal/cloud/lease.go", "internal/cloud/heartbeat.go"},
	"CTUI-0523": {"internal/adapter/adapter.go", "internal/adapter/codex/codex.go", "internal/adapter/claude/claude.go", "internal/adapter/opencode/opencode.go", "internal/adapter/antigravity/antigravity.go", "internal/harness/intelligence.go", "internal/store/harness.go"},
	"CTUI-0557": {"internal/cloud/config.go", "internal/cloud/state.go", "internal/cloud/session.go", "internal/cloud/gate.go", "internal/cloud/telemetry.go"},
}

// The original manifest remains intact for Community and generic IA loading.
// Only these independently implemented Extreme bindings resolve its old gaps.
func configureBuildIA(ia *IA) {
	// Provider quota/status observation and cloud entitlement request tracking
	// are not Extreme capabilities: Extreme runs local ULTRA with no cloud
	// entitlement and has no provider-reported quota source. The shared manifest
	// binds them for the default edition; here they stay honest gaps.
	for id, sources := range buildUnboundProviderGaps {
		if node, found := ia.Node(id); found {
			node.Binding = BindingGap
			node.BackendSources = sources
		}
	}
	if node, found := ia.Node("CTUI-0071"); found {
		node.Binding = BindingBound
		node.BackendSources = []string{"internal/app/memory_snapshot_extreme.go", "internal/store/memory_snapshot_extreme.go"}
	}
	for _, id := range []string{"CTUI-0050", "CTUI-0051", "CTUI-0052"} {
		if node, found := ia.Node(id); found {
			node.Binding = BindingBound
			node.BackendSources = []string{"internal/app/cell_controls_extreme.go", "internal/store/cell_descriptor_extreme.go"}
		}
	}
	if node, found := ia.Node("CTUI-0048"); found {
		node.Binding = BindingBound
		node.BackendSources = []string{"internal/app/goal_cancel_extreme.go", "internal/app/local_control.go"}
	}
	if node, found := ia.Node("CTUI-0073"); found {
		node.Binding = BindingBound
		node.BackendSources = []string{"internal/app/run_control.go", "internal/execution/run_control.go"}
	}
	for _, id := range []string{"CTUI-0077", "CTUI-0078", "CTUI-0081", "CTUI-0082"} {
		if node, found := ia.Node(id); found {
			node.Binding = BindingBound
			node.BackendSources = []string{"internal/tui/control_budget.go", "internal/app/goal_budget.go"}
		}
	}
	for id := range buildTaskControlOperations {
		if node, found := ia.Node(string(id)); found {
			node.Binding = BindingBound
			node.BackendSources = []string{"internal/app/task_commands.go", "internal/store/task_commands.go"}
		}
	}
}

type taskControlAuthority interface {
	CommandTask(context.Context, app.CommandEnvelope, app.TaskCommand) (model.Task, error)
}

func configureTaskControlBindings(source *ControlSource, bindings map[ActionID]Binding) {
	configureMemoryRestoreBinding(source, bindings)
	configureApprovedCellBindings(source, bindings)
	configureGoalControlBindings(source, bindings)
	configureSessionResumeBinding(source, bindings)
	for id, operation := range buildTaskControlOperations {
		binding := bindings[id]
		binding.Gap = ""
		binding.Inputs = []InputSpec{{Key: "task_id", Label: "Exact task ID", Required: true, Validate: validateTaskControlID}}
		binding.PrepareRequest = func(ctx context.Context, request ActionRequest) (Target, error) {
			return source.prepareTaskControl(ctx, request, operation)
		}
		binding.Execute = func(ctx context.Context, request ActionRequest) (Outcome, error) {
			return source.executeTaskControl(ctx, request, operation)
		}
		bindings[id] = binding
	}
}

func validateTaskControlID(id string) error { return validateCanonicalID("task ID", id) }

// canonicalIDValidator is validateTaskControlID for a field that holds some
// other canonical ID, so a refusal names what the field asks for (a cell or
// snapshot field must not say "task ID").
func canonicalIDValidator(what string) func(string) error {
	return func(id string) error { return validateCanonicalID(what, id) }
}

func validateCanonicalID(what, id string) error {
	if id == "" || len(id) > 256 || strings.ContainsFunc(id, unicode.IsSpace) || strings.ContainsFunc(id, unicode.IsControl) {
		return fmt.Errorf("a bounded canonical %s without whitespace is required", what)
	}
	return nil
}

func (s *ControlSource) prepareTaskControl(ctx context.Context, request ActionRequest, operation string) (Target, error) {
	if err := s.available(); err != nil {
		return Target{}, err
	}
	if _, ok := s.Authority.(taskControlAuthority); !ok {
		return Target{}, fmt.Errorf("the attached authority does not expose canonical task control")
	}
	id := strings.TrimSpace(request.Inputs["task_id"])
	if err := validateTaskControlID(id); err != nil {
		return Target{}, err
	}
	task, err := s.Authority.Task(ctx, id)
	if err != nil {
		return Target{}, err
	}
	if task.ID != id {
		return Target{}, ErrStaleTarget
	}
	terminal := task.Status == model.TaskMerged || task.Status == model.TaskCancelled || task.Status == model.TaskSuperseded
	eligible := !terminal && task.ControlState == ""
	if operation == "cancel" {
		// Cancellation also applies to paused or settling tasks. The runtime
		// owns signalling supervision and releasing the exact task's leases.
		eligible = !terminal
	} else if operation == "resume" {
		eligible = !terminal && task.ControlState == "paused"
	} else if operation == "retry" {
		eligible = !terminal && task.ControlState == "" && task.Status == model.TaskBlocked
	}
	if !eligible {
		return Target{}, fmt.Errorf("task %s cannot %s in state %s/%s", task.ID, operation, task.Status, task.ControlState)
	}
	actor := ""
	if task.OwnerAgentID != nil {
		actor = *task.OwnerAgentID
	}
	target := taskTarget(task, actor, fmt.Sprintf("%s: %s (%s/%s, attempt %d)", operation, task.Title, task.Status, task.ControlState, task.Attempt))
	target.Digest = fmt.Sprintf("%s/%s/%d", task.Status, task.ControlState, task.Attempt)
	return target, nil
}

func (s *ControlSource) executeTaskControl(ctx context.Context, request ActionRequest, operation string) (Outcome, error) {
	authority, ok := s.Authority.(taskControlAuthority)
	if !ok {
		return Outcome{}, ErrNoBinding
	}
	if request.Target.Kind != "task" || strings.TrimSpace(request.Inputs["task_id"]) != request.Target.ID {
		return Outcome{}, ErrStaleTarget
	}
	envelope := app.CommandEnvelope{ProjectID: request.ProjectID, SessionID: request.SessionID, TargetID: request.Target.ID,
		ExpectedVersion: request.Target.Revision, IdempotencyKey: request.IdempotencyKey}
	result, err := authority.CommandTask(ctx, envelope, app.TaskCommand{Operation: operation})
	if err != nil {
		return refusal("task control refused", err, request.Target, "internal/app/task_commands.go")
	}
	proof, err := s.Authority.Task(ctx, request.Target.ID)
	if err != nil {
		return unproven(request.Target, request.Target.ID, err)
	}
	proved := result.ID == request.Target.ID && proof.ID == request.Target.ID && proof.Revision == result.Revision && proof.Revision > request.Target.Revision &&
		proof.Status == result.Status && proof.ControlState == result.ControlState && proof.Attempt == result.Attempt
	switch operation {
	case "pause":
		proved = proved && (proof.ControlState == "paused" || proof.ControlState == "pause-requested")
	case "resume", "retry":
		proved = proved && proof.ControlState == "" && proof.Status == model.TaskReady
	case "cancel":
		proved = proved && proof.ControlState == "cancelled" && proof.Status == model.TaskCancelled && proof.OwnerAgentID == nil
	}
	state := string(proof.Status)
	if proof.ControlState != "" {
		state = proof.ControlState
	}
	target := taskTarget(proof, "", "task control read-back")
	if !proved {
		return Outcome{Verdict: VerdictUnknown, Detail: "task control returned but durable state did not prove the exact result", Target: target,
			Proof: Unknown("reread the task before assuming the action applied", "internal/app/task_commands.go")}, nil
	}
	detail := fmt.Sprintf("task %s: %s committed at revision %d (attempt %d) [%s]", proof.ID, operation, proof.Revision, proof.Attempt, state)
	if state == "pause-requested" {
		detail += "; the worker is still settling"
	} else if operation == "cancel" {
		// The canonical state proves cancellation was recorded, not that an
		// external provider has exited. Never confuse the two observations.
		detail += "; cancellation recorded, worker exit is not confirmed"
	}
	return Outcome{Verdict: VerdictPass, Detail: detail, Target: target,
		Proof: Known(detail, "internal/app/task_commands.go")}, nil
}
