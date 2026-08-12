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

type SandboxRequest struct {
	Worktree       string
	WritableDirs   []string
	WritableTmpfs  []string // sandbox-internal paths mounted as ephemeral tmpfs
	ReadOnlyBinds  []Bind
	NetworkAllowed bool
	ExtraEnv       []string // KEY=VALUE pairs forwarded into sandbox
}

type CommandSpec struct {
	Path      string
	Args      []string
	Env       []string
	Dir       string
	Isolation IsolationCapability
}
