//go:build !governed

package app

import (
	"context"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/adapter/antigravity"
	"github.com/Zen1th53/marshal/internal/adapter/claude"
	"github.com/Zen1th53/marshal/internal/adapter/codex"
	"github.com/Zen1th53/marshal/internal/adapter/opencode"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/store"
)

// requiredProviderEnvelopeBytes bounds one complete provider request that
// carries the mandatory project context: the packet maximum, so the private
// edition never refuses planning, an amendment or a worker dispatch at a lower
// size. Non-essential knowledge moves to MCP recall before this is reached.
const requiredProviderEnvelopeBytes = projectcontext.MaximumPacketBytes

// Runs before transport approval spending, secret preparation and probes.
// Preflight coverage does not permit a later provider call: final dispatch
// performs fresh admission and consumes its own live one-shot token.
func (r *Runtime) preflightRequiredProcess05Context(ctx context.Context, task execution.TaskExecution, agent, provider, modelName string, pkg execution.ConstraintPackage) error {
	text, _, err := r.requiredExecutionContext(ctx, task, agent, provider, modelName, pkg.FormatPromptHeader()+process05BriefFor(ctx, task), requiredProviderEnvelopeBytes, "admission")
	if err != nil {
		return err
	}
	// Match the core Request constructed by all four Process05 callbacks;
	// optional source hints are added only at final dispatch. Measure through
	// the same actual adapter renderer used there, including JSON escaping.
	request := adapter.Request{
		TaskID: task.CanonicalTaskID, Title: task.Description, Worktree: task.WorktreePath, Model: modelName,
		AllowedOperations: []string{"filesystem.read", "filesystem.write", "shell.execute"},
		EvidenceRequired:  append([]string(nil), pkg.VerificationObligations...), TrustedContext: text,
	}
	wire, err := requiredProcess05ProviderPrompt(provider, request)
	if err != nil {
		return err
	}
	if len(wire) > requiredProviderEnvelopeBytes {
		return projectcontext.ErrBudget
	}
	return nil
}

// The complete typed provider envelope is measured after rendering. Every
// essential remains whole; optional derived source hints are omitted first.
func (r *Runtime) prepareRequiredProcess05Request(ctx context.Context, task execution.TaskExecution, agent, provider, modelName string, request *adapter.Request, optional string) error {
	_, _, err := r.prepareRequiredProcess05Admission(ctx, task, agent, provider, modelName, request, optional)
	return err
}

func (r *Runtime) prepareWatchedRequiredProcess05Request(ctx context.Context, task execution.TaskExecution, agent, provider, modelName string, request *adapter.Request, optional string) (context.Context, func() error, error) {
	receipt, admission, err := r.prepareRequiredProcess05Admission(ctx, task, agent, provider, modelName, request, optional)
	if err != nil {
		return nil, nil, err
	}
	owned, watch, err := r.startRequiredProjectContextWatch(ctx, receipt, admission)
	if err != nil {
		return nil, nil, err
	}
	return owned, watch.Finish, nil
}

func (r *Runtime) prepareRequiredProcess05Admission(ctx context.Context, task execution.TaskExecution, agent, provider, modelName string, request *adapter.Request, optional string) (projectcontext.Receipt, store.ProjectContextAdmission, error) {
	var receipt projectcontext.Receipt
	var admission store.ProjectContextAdmission
	if request == nil || request.TaskID != task.CanonicalTaskID || request.Model != modelName || request.Worktree != task.WorktreePath || request.Title != task.Description {
		return receipt, admission, projectcontext.Changed("project_context_provider_extreme.go:73")
	}
	// The governed Marshal brief (approved plan note, return reasons) is part
	// of the admitted prefix, so it is measured and bound like the header.
	text, receipt, err := r.requiredExecutionContextWithAdmission(ctx, task, agent, provider, modelName, request.TrustedContext+process05BriefFor(ctx, task), requiredProviderEnvelopeBytes, "dispatch", &admission)
	if err != nil {
		return receipt, admission, err
	}
	current := *request
	current.TrustedContext = text
	wire, err := requiredProcess05ProviderPrompt(provider, current)
	if err != nil {
		return receipt, admission, err
	}
	if len(wire) > requiredProviderEnvelopeBytes {
		return receipt, admission, projectcontext.ErrBudget
	}
	if len(optional) <= 4096 {
		withOptional := current
		withOptional.TrustedContext += optional
		wire, err = requiredProcess05ProviderPrompt(provider, withOptional)
		if err != nil {
			return receipt, admission, err
		}
		if len(wire) <= requiredProviderEnvelopeBytes {
			current = withOptional
		}
	}
	*request = current
	return receipt, admission, nil
}

func requiredProcess05ProviderPrompt(provider string, request adapter.Request) ([]byte, error) {
	switch provider {
	case "claude":
		return claude.GovernedPrompt(request)
	case "opencode":
		return opencode.GovernedPrompt(request)
	case "agy":
		return antigravity.GovernedPrompt(request)
	default:
		// Codex's standard task schema also measures data-only composed
		// harness fixtures. Only the four actual Runtime adapter callbacks
		// above invoke providers; rendering a prompt never grants a call.
		return codex.GovernedPrompt(request)
	}
}
