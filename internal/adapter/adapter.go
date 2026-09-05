package adapter

import (
	"context"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

type Status string

const (
	StatusSuccess          Status = "success"
	StatusFailure          Status = "failure"
	StatusBlocked          Status = "blocked"
	StatusApprovalRequired Status = "approval_required"
)

type Probe struct {
	Name         string            `json:"name"`
	Available    bool              `json:"available"`
	Version      string            `json:"version"`
	Capabilities map[string]string `json:"capabilities"`
}

type Request struct {
	TaskID            string
	Title             string
	Worktree          string
	Model             string
	BaseCommit        string
	HeadCommit        string
	AllowedOperations []string
	EvidenceRequired  []string
	TrustedContext    string
	Heartbeat         func()
	HeartbeatInterval time.Duration
}

type Command struct {
	Path              string
	Args              []string
	Env               []string
	Dir               string
	Stdin             []byte
	Heartbeat         func()
	HeartbeatInterval time.Duration
}

type ProcessResult struct {
	Stdout          []byte
	Stderr          []byte
	ExitCode        int
	StartedAt       time.Time
	EndedAt         time.Time
	TimedOut        bool
	Cancelled       bool
	OutputTruncated bool
	Isolation       model.IsolationCapability
}

// Usage records normalized resource consumption reported by the harness.
// Fields are pointers or have explicit Reported flags so that unknown usage
// remains unknown, never synthesized into zero.
type Usage struct {
	Reported         bool     `json:"reported"`
	PromptTokens     *int64   `json:"prompt_tokens,omitempty"`
	CompletionTokens *int64   `json:"completion_tokens,omitempty"`
	TotalTokens      *int64   `json:"total_tokens,omitempty"`
	CostUSD          *float64 `json:"cost_usd,omitempty"`
	ModelCalls       int      `json:"model_calls,omitempty"`
	DurationMs       int64    `json:"duration_ms,omitempty"`
}

type Result struct {
	Adapter         string
	AdapterVersion  string
	SessionID       string
	Status          Status
	FinalText       string
	Events          []map[string]any
	ExitCode        int
	Stdout          []byte
	Stderr          []byte
	StartedAt       time.Time
	EndedAt         time.Time
	TimedOut        bool
	Cancelled       bool
	OutputTruncated bool
	Isolation       model.IsolationCapability
	Usage           Usage
}

type ProcessRunner interface {
	Run(context.Context, Command) (ProcessResult, error)
}

type Adapter interface {
	Probe(context.Context) (Probe, error)
	Run(context.Context, Request) (Result, error)
	Status(context.Context, string) (Status, error)
	Resume(context.Context, string, Request) (Result, error)
	Capabilities() map[string]string
	CollectEvidence(Result) map[string]any
	Shutdown(context.Context, string) error
}
