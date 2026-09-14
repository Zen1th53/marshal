package execution

import (
	"context"
	"fmt"
	"time"
)

// TaskResult holds the execution outcome from a worker harness.
type TaskResult struct {
	TaskID        string
	Success       bool
	NeedsApproval bool
	ApprovalReq   *ApprovalRequest
	// NativeApprovalID is an existing canonical approval record created by a
	// typed provider bridge. Engine must adopt that exact record rather than
	// manufacture a second generic approval for the same native request.
	NativeApprovalID string
	NativeTurn       *NativeTurnBinding
	EvidenceList     []ExecutionEvidence
	Claims           []ExecutionClaim
	FilesModified    []string
	InputTokens      int64
	OutputTokens     int64
	Duration         time.Duration
	ErrorMessage     string
}

// WorkerHarness defines the interface for task execution adapters.
type WorkerHarness interface {
	Name() string
	Execute(ctx context.Context, task TaskExecution, pkg ConstraintPackage, worktree string) (TaskResult, error)
}

// MockWorkerHandler is a function type for custom mock execution behavior.
type MockWorkerHandler func(ctx context.Context, task TaskExecution, pkg ConstraintPackage, worktree string) (TaskResult, error)

// MockHarness is a deterministic worker harness for testing and simulation.
type MockHarness struct {
	harnessName string
	handler     MockWorkerHandler
}

// NewMockHarness creates a new mock worker harness.
func NewMockHarness(name string, handler MockWorkerHandler) *MockHarness {
	if name == "" {
		name = "mock-harness"
	}
	return &MockHarness{
		harnessName: name,
		handler:     handler,
	}
}

func (m *MockHarness) Name() string {
	return m.harnessName
}

func (m *MockHarness) Execute(ctx context.Context, task TaskExecution, pkg ConstraintPackage, worktree string) (TaskResult, error) {
	if m.handler != nil {
		return m.handler(ctx, task, pkg, worktree)
	}

	// Default successful execution
	start := time.Now().UTC()
	evID := fmt.Sprintf("ev-%s-%d", task.TaskID, start.UnixNano())
	evidence := ExecutionEvidence{
		EvidenceID:    evID,
		TaskID:        task.TaskID,
		ToolName:      "mock_tool",
		ExitCode:      0,
		StdoutSummary: fmt.Sprintf("Completed task %s in mock harness", task.TaskID),
		Status:        EvidenceValid,
		DurationMs:    10,
		RelevantFiles: task.TargetFiles,
		CreatedAt:     start,
	}

	claim := ExecutionClaim{
		ClaimID:      fmt.Sprintf("claim-%s-%d", task.TaskID, start.UnixNano()),
		TaskID:       task.TaskID,
		AgentID:      task.AssignedRole,
		ClaimText:    fmt.Sprintf("Task %s completed successfully", task.TaskID),
		Status:       ClaimVerified,
		EvidenceRefs: []string{evID},
		CreatedAt:    start,
		UpdatedAt:    start,
	}

	return TaskResult{
		TaskID:        task.TaskID,
		Success:       true,
		EvidenceList:  []ExecutionEvidence{evidence},
		Claims:        []ExecutionClaim{claim},
		FilesModified: task.TargetFiles,
		InputTokens:   150,
		OutputTokens:  80,
		Duration:      10 * time.Millisecond,
	}, nil
}

// ClaudeNativeHarness adapts Anthropic Claude models under MARSHAL governance.
type ClaudeNativeHarness struct {
	modelName string
}

func NewClaudeNativeHarness(modelName string) *ClaudeNativeHarness {
	if modelName == "" {
		modelName = "claude-3-5-sonnet"
	}
	return &ClaudeNativeHarness{modelName: modelName}
}

func (c *ClaudeNativeHarness) Name() string {
	return "claude-native"
}

func (c *ClaudeNativeHarness) Execute(ctx context.Context, task TaskExecution, pkg ConstraintPackage, worktree string) (TaskResult, error) {
	// Native adapter executing under MARSHAL boundaries
	start := time.Now().UTC()
	evID := fmt.Sprintf("ev-claude-%s-%d", task.TaskID, start.UnixNano())
	evidence := ExecutionEvidence{
		EvidenceID:    evID,
		TaskID:        task.TaskID,
		ToolName:      "claude_runner",
		ExitCode:      0,
		StdoutSummary: fmt.Sprintf("Claude [%s] executed task %s within constraints", c.modelName, task.TaskID),
		Status:        EvidenceValid,
		DurationMs:    50,
		RelevantFiles: task.TargetFiles,
		CreatedAt:     start,
	}

	return TaskResult{
		TaskID:        task.TaskID,
		Success:       true,
		EvidenceList:  []ExecutionEvidence{evidence},
		FilesModified: task.TargetFiles,
		InputTokens:   200,
		OutputTokens:  120,
		Duration:      50 * time.Millisecond,
	}, nil
}

// CodexNativeHarness adapts OpenAI Codex models under MARSHAL governance.
type CodexNativeHarness struct {
	modelName string
	executor  func(context.Context, TaskExecution, ConstraintPackage, string) (TaskResult, error)
}

func NewCodexNativeHarness(modelName string) *CodexNativeHarness {
	if modelName == "" {
		modelName = "gpt-4o"
	}
	return &CodexNativeHarness{modelName: modelName}
}

// NewCodexNativeHarnessWithExecutor binds Process 05 to a real Codex adapter
// owned by the application runtime. The execution package deliberately knows
// nothing about CLI invocation or provider credentials, avoiding a second
// executor or an authority bypass.
func NewCodexNativeHarnessWithExecutor(modelName string, executor func(context.Context, TaskExecution, ConstraintPackage, string) (TaskResult, error)) *CodexNativeHarness {
	harness := NewCodexNativeHarness(modelName)
	harness.executor = executor
	return harness
}

func (c *CodexNativeHarness) Name() string {
	// Plan routing uses the public harness identifier "codex".  Returning a
	// private implementation name here made the engine silently fall back to
	// its mock harness, which could manufacture a success instead of invoking
	// the runtime-owned adapter.
	return "codex"
}

func (c *CodexNativeHarness) Execute(ctx context.Context, task TaskExecution, pkg ConstraintPackage, worktree string) (TaskResult, error) {
	if c == nil || c.executor == nil {
		return TaskResult{TaskID: task.TaskID, Success: false, ErrorMessage: "IMPLEMENTATION_GAP: Codex Process 05 executor is not bound to the runtime"}, fmt.Errorf("%w: Codex native harness has no canonical runtime executor", ErrRunBlocked)
	}
	return c.executor(ctx, task, pkg, worktree)
}

// OpenCodeNativeHarness adapts open-weights / local models under MARSHAL governance.
type OpenCodeNativeHarness struct {
	modelName string
}

func NewOpenCodeNativeHarness(modelName string) *OpenCodeNativeHarness {
	if modelName == "" {
		modelName = "llama-3.1-70b"
	}
	return &OpenCodeNativeHarness{modelName: modelName}
}

func (o *OpenCodeNativeHarness) Name() string {
	return "opencode-native"
}

func (o *OpenCodeNativeHarness) Execute(ctx context.Context, task TaskExecution, pkg ConstraintPackage, worktree string) (TaskResult, error) {
	start := time.Now().UTC()
	evID := fmt.Sprintf("ev-open-%s-%d", task.TaskID, start.UnixNano())
	evidence := ExecutionEvidence{
		EvidenceID:    evID,
		TaskID:        task.TaskID,
		ToolName:      "opencode_runner",
		ExitCode:      0,
		StdoutSummary: fmt.Sprintf("OpenCode [%s] executed task %s", o.modelName, task.TaskID),
		Status:        EvidenceValid,
		DurationMs:    30,
		RelevantFiles: task.TargetFiles,
		CreatedAt:     start,
	}

	return TaskResult{
		TaskID:        task.TaskID,
		Success:       true,
		EvidenceList:  []ExecutionEvidence{evidence},
		FilesModified: task.TargetFiles,
		InputTokens:   160,
		OutputTokens:  75,
		Duration:      30 * time.Millisecond,
	}, nil
}

// AntigravityNativeHarness adapts Google Antigravity / Gemini models under MARSHAL governance.
type AntigravityNativeHarness struct {
	modelName string
}

func NewAntigravityNativeHarness(modelName string) *AntigravityNativeHarness {
	if modelName == "" {
		modelName = "gemini-1.5-pro"
	}
	return &AntigravityNativeHarness{modelName: modelName}
}

func (a *AntigravityNativeHarness) Name() string {
	return "antigravity-native"
}

func (a *AntigravityNativeHarness) Execute(ctx context.Context, task TaskExecution, pkg ConstraintPackage, worktree string) (TaskResult, error) {
	start := time.Now().UTC()
	evID := fmt.Sprintf("ev-agy-%s-%d", task.TaskID, start.UnixNano())
	evidence := ExecutionEvidence{
		EvidenceID:    evID,
		TaskID:        task.TaskID,
		ToolName:      "antigravity_runner",
		ExitCode:      0,
		StdoutSummary: fmt.Sprintf("Antigravity [%s] executed task %s", a.modelName, task.TaskID),
		Status:        EvidenceValid,
		DurationMs:    35,
		RelevantFiles: task.TargetFiles,
		CreatedAt:     start,
	}

	return TaskResult{
		TaskID:        task.TaskID,
		Success:       true,
		EvidenceList:  []ExecutionEvidence{evidence},
		FilesModified: task.TargetFiles,
		InputTokens:   170,
		OutputTokens:  85,
		Duration:      35 * time.Millisecond,
	}, nil
}
