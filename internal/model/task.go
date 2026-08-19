package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

type TaskStatus string

const (
	TaskProposed       TaskStatus = "proposed"
	TaskReady          TaskStatus = "ready"
	TaskClaimed        TaskStatus = "claimed"
	TaskWorking        TaskStatus = "working"
	TaskBlocked        TaskStatus = "blocked"
	TaskReview         TaskStatus = "review"
	TaskQA             TaskStatus = "qa"
	TaskSecurityReview TaskStatus = "security_review"
	TaskReadyToMerge   TaskStatus = "ready_to_merge"
	TaskMerged         TaskStatus = "merged"
	TaskCancelled      TaskStatus = "cancelled"
	TaskSuperseded     TaskStatus = "superseded"
)

type Risk string

const (
	R0 Risk = "R0"
	R1 Risk = "R1"
	R2 Risk = "R2"
	R3 Risk = "R3"
)

type Task struct {
	ID           string     `json:"id"`
	Title        string     `json:"title"`
	Status       TaskStatus `json:"status"`
	Risk         Risk       `json:"risk"`
	OwnerAgentID *string    `json:"owner_agent_id,omitempty"`
	Branch       *string    `json:"branch,omitempty"`
	Worktree     *string    `json:"worktree,omitempty"`
	BaseCommit   *string    `json:"base_commit,omitempty"`
	HeadCommit   *string    `json:"head_commit,omitempty"`
	Dependencies []string   `json:"dependencies,omitempty"`
	Revision     int64      `json:"revision"`
}

type ImportResult struct {
	Added   int `json:"added"`
	Matched int `json:"matched"`
}

type ClaimRequest struct {
	TaskID           string
	AgentID          string
	SessionID        string
	ExpectedRevision int64
	ExpiresAt        time.Time
}

type ReleaseRequest struct {
	TaskID           string
	LeaseID          string
	SessionID        string
	AgentID          string
	ExpectedRevision int64
	BlockedReason    string
}

type TaskTransitionRequest struct {
	TaskID           string     `json:"task_id"`
	FromStatus       TaskStatus `json:"from_status"`
	ToStatus         TaskStatus `json:"to_status"`
	ActorRole        Role       `json:"actor_role"`
	ActorID          string     `json:"actor_id"`
	HeadCommit       string     `json:"head_commit,omitempty"`
	Reason           string     `json:"reason,omitempty"`
	ExpectedRevision int64      `json:"expected_revision"`
}

type Lease struct {
	ID         string    `json:"id"`
	TaskID     string    `json:"task_id"`
	SessionID  string    `json:"session_id"`
	AcquiredAt time.Time `json:"acquired_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	Revision   int64     `json:"revision"`
	Status     string    `json:"status"`
}

type ActiveLease struct {
	Lease        Lease  `json:"lease"`
	AgentID      string `json:"agent_id"`
	TaskRevision int64  `json:"task_revision"`
}

var taskIDPattern = regexp.MustCompile(`^TASK-[A-Za-z0-9._-]+$`)

func DecodeTasks(r io.Reader) ([]Task, error) {
	data, err := io.ReadAll(io.LimitReader(r, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("%w: read task JSON: %v", ErrInvalid, err)
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("%w: empty task JSON", ErrInvalid)
	}
	var tasks []Task
	switch trimmed[0] {
	case '{':
		var task Task
		if err := decodeStrict(trimmed, &task); err != nil {
			return nil, err
		}
		tasks = []Task{task}
	case '[':
		if err := decodeStrict(trimmed, &tasks); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("%w: task JSON must be an object or array", ErrInvalid)
	}
	if len(tasks) == 0 {
		return nil, fmt.Errorf("%w: task array is empty", ErrInvalid)
	}
	for i := range tasks {
		if err := tasks[i].Validate(); err != nil {
			return nil, fmt.Errorf("task %d: %w", i, err)
		}
	}
	return tasks, nil
}

func decodeStrict(data []byte, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return fmt.Errorf("%w: decode task JSON: %v", ErrInvalid, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("%w: trailing task JSON", ErrInvalid)
		}
		return fmt.Errorf("%w: trailing task JSON: %v", ErrInvalid, err)
	}
	return nil
}

func (t Task) Validate() error {
	if !taskIDPattern.MatchString(t.ID) {
		return fmt.Errorf("%w: invalid task ID %q", ErrInvalid, t.ID)
	}
	if strings.TrimSpace(t.Title) == "" {
		return fmt.Errorf("%w: task title is empty", ErrInvalid)
	}
	if !validTaskStatus(t.Status) {
		return fmt.Errorf("%w: invalid task status %q", ErrInvalid, t.Status)
	}
	if t.Risk != R0 && t.Risk != R1 && t.Risk != R2 && t.Risk != R3 {
		return fmt.Errorf("%w: invalid task risk %q", ErrInvalid, t.Risk)
	}
	if t.Revision < 0 {
		return fmt.Errorf("%w: negative task revision", ErrInvalid)
	}
	seen := make(map[string]struct{}, len(t.Dependencies))
	for _, dependency := range t.Dependencies {
		if !taskIDPattern.MatchString(dependency) {
			return fmt.Errorf("%w: invalid dependency ID %q", ErrInvalid, dependency)
		}
		if dependency == t.ID {
			return fmt.Errorf("%w: task depends on itself", ErrInvalid)
		}
		if _, exists := seen[dependency]; exists {
			return fmt.Errorf("%w: duplicate dependency %q", ErrInvalid, dependency)
		}
		seen[dependency] = struct{}{}
	}
	return nil
}

func validTaskStatus(status TaskStatus) bool {
	switch status {
	case TaskProposed, TaskReady, TaskClaimed, TaskWorking, TaskBlocked,
		TaskReview, TaskQA, TaskSecurityReview, TaskReadyToMerge, TaskMerged,
		TaskCancelled, TaskSuperseded:
		return true
	default:
		return false
	}
}

func ValidateTaskTransition(from, to TaskStatus, risk Risk, actor Role, currentCommit, reqCommit string) error {
	if !validTaskStatus(from) || !validTaskStatus(to) {
		return fmt.Errorf("%w: invalid task status transition %s -> %s", ErrInvalid, from, to)
	}
	if from == to {
		return fmt.Errorf("%w: self transition not allowed", ErrInvalid)
	}

	// Terminal states cannot transition
	if from == TaskMerged || from == TaskCancelled || from == TaskSuperseded {
		return fmt.Errorf("%w: cannot transition out of terminal state %s", ErrConflict, from)
	}

	// Any state can transition to cancelled with authorized role
	if to == TaskCancelled {
		if actor == RoleDeveloper || actor == RoleArchitect || actor == RoleAdmin || actor == RoleOrchestrator {
			return nil
		}
		return fmt.Errorf("%w: unauthorized role %s for cancellation", ErrUnauthorized, actor)
	}

	switch from {
	case TaskProposed:
		if to == TaskReady {
			if actor == RoleArchitect || actor == RoleAdmin || actor == RoleOrchestrator || actor == RoleDeveloper {
				return nil
			}
			return fmt.Errorf("%w: unauthorized role %s for proposed -> ready", ErrUnauthorized, actor)
		}

	case TaskReady:
		if to == TaskClaimed {
			return nil // handled by ClaimTask
		}
		if to == TaskBlocked {
			return nil
		}
		if to == TaskSuperseded {
			if actor == RoleArchitect || actor == RoleAdmin {
				return nil
			}
			return fmt.Errorf("%w: unauthorized role %s for superseding task", ErrUnauthorized, actor)
		}

	case TaskClaimed:
		if to == TaskWorking || to == TaskReady || to == TaskBlocked {
			return nil
		}

	case TaskWorking:
		if to == TaskReview {
			if currentCommit != "" && reqCommit != "" && currentCommit != reqCommit {
				return fmt.Errorf("%w: commit mismatch during review submission", ErrConflict)
			}
			return nil
		}
		if to == TaskBlocked || to == TaskReady {
			return nil
		}

	case TaskBlocked:
		if to == TaskReady || to == TaskWorking {
			return nil
		}

	case TaskReview:
		// Reviewer / Architect approves review -> QA
		if to == TaskQA {
			if actor != RoleReviewer && actor != RoleArchitect && actor != RoleAdmin {
				return fmt.Errorf("%w: review approval requires reviewer, architect, or admin role (got %s)", ErrUnauthorized, actor)
			}
			if currentCommit != "" && reqCommit != "" && currentCommit != reqCommit {
				return fmt.Errorf("%w: stale review approval: head commit %s does not match request commit %s", ErrConflict, currentCommit, reqCommit)
			}
			return nil
		}
		// Reviewer requests changes -> back to working
		if to == TaskWorking {
			if actor == RoleReviewer || actor == RoleArchitect || actor == RoleAdmin || actor == RoleDeveloper {
				return nil
			}
			return fmt.Errorf("%w: unauthorized role %s for review rejection", ErrUnauthorized, actor)
		}
		if to == TaskBlocked {
			return nil
		}

	case TaskQA:
		// Rejection -> back to working
		if to == TaskWorking {
			if actor == RoleQA || actor == RoleArchitect || actor == RoleAdmin {
				return nil
			}
			return fmt.Errorf("%w: unauthorized role %s for QA rejection", ErrUnauthorized, actor)
		}
		// QA passes -> Security Review (for R2/R3)
		if to == TaskSecurityReview {
			if actor != RoleQA && actor != RoleArchitect && actor != RoleAdmin {
				return fmt.Errorf("%w: QA transition requires qa, architect, or admin role (got %s)", ErrUnauthorized, actor)
			}
			if currentCommit != "" && reqCommit != "" && currentCommit != reqCommit {
				return fmt.Errorf("%w: stale QA approval: head commit %s does not match request commit %s", ErrConflict, currentCommit, reqCommit)
			}
			return nil
		}
		if to == TaskReadyToMerge {
			// High risk tasks (R2, R3) MUST pass security_review first!
			if risk == R2 || risk == R3 {
				return fmt.Errorf("%w: high-risk task (%s) requires security_review before ready_to_merge", ErrConflict, risk)
			}
			if actor != RoleQA && actor != RoleArchitect && actor != RoleAdmin {
				return fmt.Errorf("%w: QA pass requires qa, architect, or admin role (got %s)", ErrUnauthorized, actor)
			}
			if currentCommit != "" && reqCommit != "" && currentCommit != reqCommit {
				return fmt.Errorf("%w: stale QA approval: head commit %s does not match request commit %s", ErrConflict, currentCommit, reqCommit)
			}
			return nil
		}
		if to == TaskBlocked {
			return nil
		}

	case TaskSecurityReview:
		// AppSec approves -> ready_to_merge
		if to == TaskReadyToMerge {
			if actor != RoleAppSec && actor != RoleAdmin {
				return fmt.Errorf("%w: security approval requires appsec or admin role (got %s)", ErrUnauthorized, actor)
			}
			if currentCommit != "" && reqCommit != "" && currentCommit != reqCommit {
				return fmt.Errorf("%w: stale security approval: head commit %s does not match request commit %s", ErrConflict, currentCommit, reqCommit)
			}
			return nil
		}
		// Security findings -> back to working
		if to == TaskWorking {
			if actor == RoleAppSec || actor == RoleAdmin || actor == RoleArchitect {
				return nil
			}
			return fmt.Errorf("%w: unauthorized role %s for security rejection", ErrUnauthorized, actor)
		}
		if to == TaskBlocked {
			return nil
		}

	case TaskReadyToMerge:
		// Merge execution -> merged
		if to == TaskMerged {
			if actor != RoleOrchestrator && actor != RoleAdmin && actor != RoleArchitect {
				return fmt.Errorf("%w: merge execution requires orchestrator, admin, or architect role (got %s)", ErrUnauthorized, actor)
			}
			if currentCommit != "" && reqCommit != "" && currentCommit != reqCommit {
				return fmt.Errorf("%w: stale merge execution: head commit %s does not match request commit %s", ErrConflict, currentCommit, reqCommit)
			}
			return nil
		}
		// Conflict during merge attempt -> back to working
		if to == TaskWorking || to == TaskBlocked {
			return nil
		}
	}

	return fmt.Errorf("%w: illegal direct transition from %s to %s", ErrConflict, from, to)
}
