package model

import "time"

type Operation string

const (
	FilesystemRead       Operation = "filesystem.read"
	FilesystemWrite      Operation = "filesystem.write"
	ShellExecute         Operation = "shell.execute"
	NetworkAccess        Operation = "network.access"
	GitCommit            Operation = "git.commit"
	GitPush              Operation = "git.push"
	GitHistoryRewrite    Operation = "git.history_rewrite"
	SecretRead           Operation = "secret.read"
	ExternalUpload       Operation = "external.upload"
	Deploy               Operation = "deploy"
	DestructiveOperation Operation = "destructive_operation"
)

type Decision string

const (
	Allow           Decision = "ALLOW"
	Deny            Decision = "DENY"
	RequireApproval Decision = "REQUIRE_APPROVAL"
)

type PolicyInput struct {
	AgentID            string
	SessionID          string
	Role               Role
	TaskID             string
	Risk               Risk
	Operation          Operation
	Target             string
	Environment        string
	TaskOwned          bool
	TargetInScope      bool
	Required           bool
	ExplicitPermission bool
	Production         bool
	ApprovalValid      bool
}

type PolicyDecision struct {
	Decision Decision `json:"decision"`
	Reason   string   `json:"reason"`
	Rule     string   `json:"policy_rule"`
}

type ApprovalStatus string

const (
	ApprovalRequested ApprovalStatus = "requested"
	ApprovalApproved  ApprovalStatus = "approved"
	ApprovalDenied    ApprovalStatus = "denied"
	ApprovalExpired   ApprovalStatus = "expired"
	ApprovalConsumed  ApprovalStatus = "consumed"
	ApprovalRevoked   ApprovalStatus = "revoked"
)

type Approval struct {
	ID          string
	ProjectID   string
	Operation   Operation
	Scope       string
	Target      string
	RequestedBy string
	ApprovedBy  string
	Status      ApprovalStatus
	Commit      string
	Conditions  []string
	CreatedAt   time.Time
	ExpiresAt   *time.Time
	Revision    int64
}

type ApprovalUse struct {
	ID               string
	Operation        Operation
	Scope            string
	Target           string
	Commit           string
	Now              time.Time
	ExpectedRevision int64
}

type FindingStatus string

const (
	FindingOpen           FindingStatus = "open"
	FindingAssigned       FindingStatus = "assigned"
	FindingFixing         FindingStatus = "fixing"
	FindingReadyForRetest FindingStatus = "ready_for_retest"
	FindingClosed         FindingStatus = "closed"
	FindingAcceptedRisk   FindingStatus = "accepted_risk"
)

type Finding struct {
	ID        string
	ProjectID string
	OwnerRole Role
	Severity  string
	Status    FindingStatus
	TaskID    *string
	Title     string
	Evidence  []string
	Revision  int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

type FindingTransition struct {
	ID               string
	ActorRole        Role
	Status           FindingStatus
	ExpectedRevision int64
}

type MemoryRecord struct {
	ID             string
	ProjectID      string
	Type           string
	Status         string
	Confidence     string
	Body           string
	Provenance     map[string]any
	LastVerifiedAt *time.Time
	Revision       int64
	CreatedAt      time.Time
	UpdatedAt      time.Time
}
