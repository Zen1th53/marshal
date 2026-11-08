package adapter

import (
	"context"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/sandbox"
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
	TaskID   string
	Title    string
	Worktree string
	Model    string
	// Effort is a reasoning effort the model's catalog advertises, or empty
	// for the model's own default.
	Effort            string
	BaseCommit        string
	HeadCommit        string
	AllowedOperations []string
	EvidenceRequired  []string
	TrustedContext    string
	Heartbeat         func()
	HeartbeatInterval time.Duration
	// Idle policy for progress-based liveness; zero disables it.
	StallAfter  time.Duration
	IdleTimeout time.Duration
	Stalled     func(idle time.Duration)
}

// WorkerLiveness builds the command liveness policy for a request.
func WorkerLiveness(request Request, classify func([]byte) StreamSignal) *Liveness {
	return &Liveness{Classify: classify, DoneGrace: DefaultDoneGrace,
		StallAfter: request.StallAfter, IdleTimeout: request.IdleTimeout, Stalled: request.Stalled}
}

type Command struct {
	// StallWindow supervises silent worker turns; zero disables the watchdog.
	StallWindow       time.Duration
	StallScanInterval time.Duration
	StallWorktree     string
	// OutputObserver receives bytes before capture limits are applied.
	OutputObserver    func(string, []byte)
	Supervised        bool
	SupervisorSocket  string
	Refusal           func(context.Context, sandbox.Refusal) error
	Path              string
	Args              []string
	Env               []string
	Dir               string
	Stdin             []byte
	Heartbeat         func()
	HeartbeatInterval time.Duration
	// Liveness, when set, supervises the worker by progress and by an
	// explicit completion event instead of process existence alone.
	Liveness *Liveness
}

// StreamSignal classifies one provider stdout line.
type StreamSignal int

const (
	SignalNone StreamSignal = iota
	SignalToolStart
	SignalToolEnd
	SignalDone
)

// DefaultDoneGrace is how long a worker may keep running after its final
// stream event before the process group is stopped.
const DefaultDoneGrace = 20 * time.Second

// Liveness configures progress-based worker supervision.
type Liveness struct {
	Classify    func(line []byte) StreamSignal
	DoneGrace   time.Duration
	StallAfter  time.Duration
	IdleTimeout time.Duration
	Stalled     func(idle time.Duration)
}

type ProcessResult struct {
	Stalled          bool
	StallReason      string
	DoneSeen         bool
	StoppedAfterDone bool
	IdleTimedOut     bool
	Stdout           []byte
	Stderr           []byte
	ExitCode         int
	StartedAt        time.Time
	EndedAt          time.Time
	TimedOut         bool
	Cancelled        bool
	OutputTruncated  bool
	Isolation        model.IsolationCapability
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
	// NativeEnvelope is private governed parser metadata, never native text.
	// Empty leaves legacy/default result serialization unchanged.
	NativeEnvelope string `json:"-"`

	Adapter         string
	AdapterVersion  string
	Model           string
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
	// StoppedAfterDone: the worker emitted its final event but did not exit
	// within the grace period, so it was stopped. IdleTimedOut: no progress.
	StoppedAfterDone bool
	IdleTimedOut     bool
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
