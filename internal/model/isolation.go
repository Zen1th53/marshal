package model

type WorktreeRequest struct {
	TaskID     string
	Branch     string
	BaseCommit string
}

type Worktree struct {
	TaskID string
	Path   string
	Branch string
	HEAD   string
	Dirty  bool
}

type WorktreeState struct {
	Path   string
	Branch string
	HEAD   string
	Dirty  bool
}

type IsolationLevel string

const (
	IsolationBwrap       IsolationLevel = "bwrap"
	IsolationProcessOnly IsolationLevel = "process_only"
	IsolationBlocked     IsolationLevel = "blocked"
)

type IsolationCapability struct {
	Level      IsolationLevel `json:"level"`
	Available  bool           `json:"available"`
	Filesystem bool           `json:"filesystem"`
	Process    bool           `json:"process"`
	Network    bool           `json:"network_allowed"`
	Reason     string         `json:"reason,omitempty"`
}

type Bind struct {
	Source string
	Target string
}

// OutsideRead is one read-only host folder and the paths hidden inside it:
// MaskDirs become empty tmpfs mounts, MaskFiles are bound to /dev/null.
type OutsideRead struct {
	Folder    string
	MaskDirs  []string
	MaskFiles []string
}

type SandboxRequest struct {
	WorkerMCPMask    WorkerMCPMask // opaque invocation-owned empty workspace MCP mask
	Supervised       bool
	SupervisorBinary string
	SupervisorSocket string
	ScratchHome      string // private runtime-owned HOME outside the repository
	ReadOnlyWorktree bool
	Worktree         string
	WritableDirs     []string
	WritableTmpfs    []string // sandbox-internal paths mounted as ephemeral tmpfs
	ReadOnlyBinds    []Bind
	// OutsideReads are host folders outside the project mounted read-only at
	// their own path before the worktree (Extreme only; Community never sets
	// them). Masks hide credential stores and runtime sockets inside them.
	OutsideReads   []OutsideRead
	NetworkAllowed bool
	EgressSocket   string // runtime-owned Unix proxy socket; never a host TCP listener
	BridgeBinary   string
	ExtraEnv       []string // KEY=VALUE pairs forwarded into sandbox
}

type CommandSpec struct {
	Path      string
	Args      []string
	Env       []string
	Dir       string
	Isolation IsolationCapability
}
