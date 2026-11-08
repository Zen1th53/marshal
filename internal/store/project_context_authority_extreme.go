//go:build !governed

package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/capability"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/policy"
	"github.com/Zen1th53/marshal/internal/projectcontext"
)

// Purpose discriminates canonical data requirements. It is never an owner
// assertion or a grant: Runtime must independently authenticate and authorize.
type ProjectContextAuthorityPurpose string

const (
	ProjectContextAuthorityWorker     ProjectContextAuthorityPurpose = "worker"
	ProjectContextAuthorityPlanning   ProjectContextAuthorityPurpose = "planning"
	ProjectContextAuthorityCompletion ProjectContextAuthorityPurpose = "completion"
)

type ProjectContextAuthorityRequest struct {
	Binding              projectcontext.Binding
	RuntimePolicyID      string
	RuntimePolicyVersion int64
	Purpose              ProjectContextAuthorityPurpose
}

// Observation records exact current canonical state, including absence. Counts
// and Purpose do not authorize anything, including a zero-row owner request.
type ProjectContextAuthorityObservation struct {
	Request                                                        ProjectContextAuthorityRequest
	Digest                                                         string
	CheckedAt                                                      time.Time
	ValidUntil                                                     *time.Time
	ActiveRoleBindings, ActiveCapabilityGrants, ActiveTaskSessions int
}

func normalizeProjectAuthorityBinding(b projectcontext.Binding) projectcontext.Binding {
	b.InventoryDigest, b.ManifestDigest, b.RequiredStateDigest = "", "", ""
	return b
}

func (s *Store) ReadProjectContextAuthority(ctx context.Context, request ProjectContextAuthorityRequest, now time.Time) (ProjectContextAuthorityObservation, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ProjectContextAuthorityObservation{}, err
	}
	defer tx.Rollback()
	o, err := readProjectContextAuthorityTx(ctx, tx, request, now)
	if err != nil {
		return ProjectContextAuthorityObservation{}, err
	}
	return o, tx.Commit()
}

type projectAuthorityRow map[string]any

// Queries return bounded detached metadata only. Never call a Store read method
// here: MaxOpenConns(1) would deadlock while the admission transaction is held.
func projectAuthorityRows(ctx context.Context, tx *sql.Tx, query string, args ...any) ([]projectAuthorityRow, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	result := []projectAuthorityRow{}
	bytesRead := 0
	for rows.Next() {
		if len(result) >= maxProjectContextRefs {
			return nil, fmt.Errorf("%w: current authority inventory exceeds %d", projectcontext.ErrBudget, maxProjectContextRefs)
		}
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err = rows.Scan(pointers...); err != nil {
			return nil, err
		}
		r := projectAuthorityRow{}
		for i, k := range columns {
			if bytes, ok := values[i].([]byte); ok {
				values[i] = string(bytes)
			}
			if text, ok := values[i].(string); ok {
				bytesRead += len(text)
				if len(text) > 256<<10 || bytesRead > 1<<20 {
					return nil, projectcontext.ErrBudget
				}
			}
			r[k] = values[i]
		}
		result = append(result, r)
	}
	return result, rows.Err()
}
func authorityText(r projectAuthorityRow, k string) string { value, _ := r[k].(string); return value }
func authorityInt(r projectAuthorityRow, k string) int64   { value, _ := r[k].(int64); return value }
func authorityTime(r projectAuthorityRow, k string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, authorityText(r, k))
	if err != nil || t.IsZero() {
		return time.Time{}, projectcontext.Changed("project_context_authority_extreme.go:118")
	}
	return t.UTC(), nil
}
func authorityBoundary(o *ProjectContextAuthorityObservation, t time.Time, now time.Time) {
	if t.After(now) && (o.ValidUntil == nil || t.Before(*o.ValidUntil)) {
		copy := t.UTC()
		o.ValidUntil = &copy
	}
}

// Exact ordered keys for Store's canonical UTC RFC3339Nano timestamps. Unlike
// raw RFC3339 lexical comparison or SQLite julianday, this retains nanoseconds
// and compares "00Z" with "00.000000001Z" correctly. Historical expired rows
// are excluded in SQL before their potentially large scope payloads are read.
const projectAuthorityExpiryKey = `(substr(expires_at,1,19)||substr((CASE WHEN substr(expires_at,20,1)='.' THEN substr(expires_at,21,length(expires_at)-21) ELSE '' END)||'000000000',1,9))`

func readProjectContextAuthorityTx(ctx context.Context, tx *sql.Tx, req ProjectContextAuthorityRequest, now time.Time) (ProjectContextAuthorityObservation, error) {
	req.Binding = normalizeProjectAuthorityBinding(req.Binding)
	b := req.Binding
	o := ProjectContextAuthorityObservation{Request: req, CheckedAt: now.UTC()}
	if now.IsZero() || b.Project.CanonicalID == "" || b.Project.StorageID == "" || b.Principal == "" || b.Session == "" || b.GoalID == "" || b.GoalRevision < 1 || (req.RuntimePolicyID == "") != (req.RuntimePolicyVersion == 0) || req.RuntimePolicyVersion < 0 {
		return o, projectcontext.ErrInvalid
	}
	switch req.Purpose {
	case ProjectContextAuthorityWorker:
		if b.Provider == "" || b.CanonicalTaskID == "" || b.Attempt < 1 {
			return o, projectcontext.ErrInvalid
		}
	case ProjectContextAuthorityPlanning, ProjectContextAuthorityCompletion:
		if b.Provider != "" {
			return o, projectcontext.ErrInvalid
		}
	default:
		return o, projectcontext.ErrInvalid
	}
	if (b.TaskID == "") != (b.CanonicalTaskID == "") {
		return o, projectcontext.ErrInvalid
	}
	if _, _, _, err := readProjectContract(ctx, tx, b.Project); err != nil {
		return o, err
	}
	var goalID string
	var revision int64
	if err := tx.QueryRowContext(ctx, `SELECT active_goal_id,active_revision FROM goal_active WHERE session_id=?`, b.Session).Scan(&goalID, &revision); err != nil || goalID != b.GoalID || revision != b.GoalRevision {
		return o, projectcontext.Changed("project_context_authority_extreme.go:163")
	}
	goal, err := readGoalContract(ctx, tx, goalID, revision)
	if err != nil {
		return o, err
	}
	if goal.ProjectID != b.Project.CanonicalID && goal.ProjectID != b.Project.StorageID {
		return o, projectcontext.Changed("project_context_authority_extreme.go:170")
	}

	state := map[string]any{"request": req}
	agents, err := projectAuthorityRows(ctx, tx, `SELECT agent_id,project_id,role,COALESCE(model_provider,'') AS provider,COALESCE(model_name,'') AS configured_model,capabilities_json,status,revision FROM agents WHERE agent_id=? LIMIT 2`, b.Principal)
	if err != nil {
		return o, err
	}
	if len(agents) == 0 && req.Purpose == ProjectContextAuthorityWorker {
		return o, projectcontext.Changed("project_context_authority_extreme.go:179")
	}
	if len(agents) > 0 {
		a := agents[0]
		if authorityText(a, "project_id") != b.Project.StorageID || authorityText(a, "status") == "disabled" || (req.Purpose == ProjectContextAuthorityWorker && authorityText(a, "provider") != b.Provider) {
			return o, projectcontext.Changed("project_context_authority_extreme.go:184")
		}
		var caps []string
		if json.Unmarshal([]byte(authorityText(a, "capabilities_json")), &caps) != nil || !model.Role(authorityText(a, "role")).Valid() || authorityInt(a, "revision") < 0 {
			return o, projectcontext.Changed("project_context_authority_extreme.go:188")
		}
		sort.Strings(caps)
		a["capabilities_json"] = caps
	}
	state["agent"] = agents
	scopes := []string{b.Project.CanonicalID, b.Project.StorageID, "project:" + b.Project.CanonicalID, "project:" + b.Project.StorageID}
	if b.CanonicalTaskID != "" {
		if err = validateProjectContextPlan(ctx, tx, b); err != nil {
			return o, err
		}
		tasks, e := projectAuthorityRows(ctx, tx, `SELECT t.task_id,t.project_id,t.title,t.status,t.risk,COALESCE(t.owner_agent_id,'') AS owner,COALESCE(t.branch,'') AS branch,COALESCE(t.worktree,'') AS worktree,COALESCE(t.base_commit,'') AS base_commit,COALESCE(t.head_commit,'') AS head_commit,t.revision,c.state AS control_state,c.attempt,c.session_id,c.goal_id,c.goal_revision,c.plan_id,c.plan_version FROM tasks t JOIN task_controls c ON c.task_id=t.task_id WHERE t.task_id=? AND t.project_id=? LIMIT 2`, b.CanonicalTaskID, b.Project.StorageID)
		if e != nil {
			return o, e
		}
		if len(tasks) != 1 {
			return o, projectcontext.Changed("project_context_authority_extreme.go:204")
		}
		task := tasks[0]
		// Binding.Attempt is an engine admission counter. Transport preflight
		// and resume can increment it within one canonical task attempt. Keep
		// the current canonical attempt in state["task"] below, where its exact
		// value participates in the authority digest and retry drift check.
		if authorityText(task, "session_id") != b.Session || authorityText(task, "goal_id") != b.GoalID || authorityInt(task, "goal_revision") != b.GoalRevision || authorityText(task, "plan_id") != b.PlanID || authorityInt(task, "plan_version") != b.PlanVersion || authorityInt(task, "attempt") < 1 || authorityText(task, "control_state") != "" {
			return o, projectcontext.Changed("project_context_authority_extreme.go:212")
		}
		status := model.TaskStatus(authorityText(task, "status"))
		if status == model.TaskCancelled || status == model.TaskSuperseded || status == model.TaskBlocked || (req.Purpose == ProjectContextAuthorityWorker && status == model.TaskMerged) {
			return o, projectcontext.Changed("project_context_authority_extreme.go:216")
		}
		if req.Purpose == ProjectContextAuthorityWorker && authorityText(task, "owner") != "" && authorityText(task, "owner") != b.Principal {
			return o, projectcontext.Changed("project_context_authority_extreme.go:219")
		}
		var raw string
		if err = tx.QueryRowContext(ctx, `SELECT plan_json FROM execution_plans WHERE plan_id=? AND version=?`, b.PlanID, b.PlanVersion).Scan(&raw); err != nil {
			return o, err
		}
		p, e := decodePlan(raw)
		if e != nil {
			return o, e
		}
		found := false
		for _, pt := range p.Tasks {
			if pt.ID == b.TaskID && pt.Title == authorityText(task, "title") {
				found = true
				break
			}
		}
		if !found {
			return o, projectcontext.Changed("project_context_authority_extreme.go:237")
		}
		state["task"] = task
		dependencies, e := projectAuthorityRows(ctx, tx, `SELECT depends_on_task_id,kind FROM task_dependencies WHERE task_id=? ORDER BY depends_on_task_id LIMIT 257`, b.CanonicalTaskID)
		if e != nil {
			return o, e
		}
		state["dependencies"] = dependencies
		scopes = append(scopes, b.CanonicalTaskID, b.TaskID, "task:"+b.CanonicalTaskID, "task:"+b.TaskID)
	}
	marks := strings.TrimSuffix(strings.Repeat("?,", len(scopes)), ",")
	args := []any{b.Principal}
	for _, v := range scopes {
		args = append(args, v)
	}
	roles, err := projectAuthorityRows(ctx, tx, `SELECT binding_id,principal_id,role,scope_id,bound_by,bound_at,policy_digest FROM role_bindings WHERE principal_id=? AND scope_id IN (`+marks+`) AND revoked_at IS NULL ORDER BY binding_id LIMIT 257`, args...)
	if err != nil {
		return o, err
	}
	for _, r := range roles {
		bound, e := authorityTime(r, "bound_at")
		if e != nil {
			return o, e
		}
		role := authz.RoleBinding{ID: authorityText(r, "binding_id"), PrincipalID: b.Principal, Role: authorityText(r, "role"), ScopeID: authorityText(r, "scope_id"), BoundBy: authorityText(r, "bound_by"), BoundAt: bound, PolicyDigest: authorityText(r, "policy_digest")}
		if role.Validate() != nil {
			return o, projectcontext.Changed("project_context_authority_extreme.go:263")
		}
		active := !now.Before(bound)
		r["active"] = active
		if active {
			o.ActiveRoleBindings++
		}
		authorityBoundary(&o, bound, now)
	}
	state["roles"] = roles
	grantArgs := append([]any{}, args...)
	grantArgs = append(grantArgs, now.UTC().Format("2006-01-02T15:04:05")+fmt.Sprintf("%09d", now.Nanosecond()))
	grants, err := projectAuthorityRows(ctx, tx, `SELECT id,subject,task_id,kind,resource,actions_json,constraints_json,issuer,issued_at,expires_at,policy_digest,idempotency_key FROM capability_grants WHERE subject=? AND task_id IN (`+marks+`) AND revoked_at IS NULL AND `+projectAuthorityExpiryKey+`>? ORDER BY id LIMIT 257`, grantArgs...)
	if err != nil {
		return o, err
	}
	for _, r := range grants {
		issued, e := authorityTime(r, "issued_at")
		if e != nil {
			return o, e
		}
		expires, e := authorityTime(r, "expires_at")
		if e != nil || !now.Before(expires) {
			return o, projectcontext.Changed("project_context_authority_extreme.go:286")
		}
		g := capability.Grant{ID: capability.GrantID(authorityText(r, "id")), Subject: capability.SubjectID(b.Principal), TaskID: capability.TaskID(authorityText(r, "task_id")), Kind: capability.CapabilityKind(authorityText(r, "kind")), Scope: capability.Scope{Resource: authorityText(r, "resource")}, Issuer: capability.SubjectID(authorityText(r, "issuer")), IssuedAt: issued, ExpiresAt: expires, PolicyDigest: authorityText(r, "policy_digest"), IdempotencyKey: authorityText(r, "idempotency_key")}
		if json.Unmarshal([]byte(authorityText(r, "actions_json")), &g.Scope.Actions) != nil || json.Unmarshal([]byte(authorityText(r, "constraints_json")), &g.Scope.Constraints) != nil || g.Validate() != nil || (g.PolicyDigest != "" && policy.PolicyDigest(g.PolicyDigest).Validate() != nil) {
			return o, projectcontext.Changed("project_context_authority_extreme.go:290")
		}
		sort.Strings(g.Scope.Actions)
		r["actions_json"], r["constraints_json"] = g.Scope.Actions, g.Scope.Constraints
		active := !now.Before(issued)
		r["active"] = active
		if active {
			o.ActiveCapabilityGrants++
		}
		authorityBoundary(&o, issued, now)
		authorityBoundary(&o, expires, now)
	}
	state["grants"] = grants
	// Session presence can authorize memory in the existing service; heartbeat
	// and heartbeat revision are deliberately excluded from semantic currentness.
	sessions, err := projectAuthorityRows(ctx, tx, `SELECT session_id,agent_id,project_id,task_id,role,COALESCE(branch,'') AS branch,COALESCE(worktree,'') AS worktree,started_at,status FROM sessions WHERE agent_id=? AND project_id=? AND (task_id=? OR session_id=?) AND status='active' ORDER BY session_id LIMIT 257`, b.Principal, b.Project.StorageID, b.CanonicalTaskID, b.Session)
	if err != nil {
		return o, err
	}
	for _, r := range sessions {
		start, e := authorityTime(r, "started_at")
		if e != nil || !model.Role(authorityText(r, "role")).Valid() {
			return o, projectcontext.Changed("project_context_authority_extreme.go:312")
		}
		active := !now.Before(start)
		r["active"] = active
		if active {
			o.ActiveTaskSessions++
		}
		authorityBoundary(&o, start, now)
	}
	state["sessions"] = sessions
	policies, err := projectAuthorityRows(ctx, tx, `SELECT policy_id,version,digest,generation,source_ref,normalized_rules,status,state,COALESCE(activated_at,'') AS activated_at FROM policy_versions WHERE state='active' ORDER BY policy_id,version LIMIT 2`)
	if err != nil {
		return o, err
	}
	if len(policies) > 1 || (req.RuntimePolicyID != "" && len(policies) != 1) {
		return o, projectcontext.Changed("project_context_authority_extreme.go:327")
	}
	for _, r := range policies {
		p, e := policy.Parse([]byte(authorityText(r, "normalized_rules")))
		if e != nil {
			return o, projectcontext.Changed("project_context_authority_extreme.go:332")
		}
		canonical, e := p.CanonicalJSON()
		digest, de := p.Digest()
		if e != nil || de != nil || string(canonical) != authorityText(r, "normalized_rules") || string(digest) != authorityText(r, "digest") || string(p.ID) != authorityText(r, "policy_id") || int64(p.Version) != authorityInt(r, "version") || authorityInt(r, "generation") < 0 || len(authorityText(r, "source_ref")) > 1024 {
			return o, projectcontext.Changed("project_context_authority_extreme.go:337")
		}
		if req.RuntimePolicyID != "" && (authorityText(r, "policy_id") != req.RuntimePolicyID || authorityInt(r, "version") != req.RuntimePolicyVersion) {
			return o, projectcontext.Changed("project_context_authority_extreme.go:340")
		}
		if authorityText(r, "activated_at") != "" {
			start, e := authorityTime(r, "activated_at")
			if e != nil {
				return o, e
			}
			r["active_now"] = !now.Before(start)
			authorityBoundary(&o, start, now)
		}
	}
	state["policy"] = policies
	raw, err := json.Marshal(state)
	if err != nil {
		return o, err
	}
	if len(raw) > 4<<20 {
		return o, projectcontext.ErrBudget
	}
	digest := sha256.Sum256(raw)
	o.Digest = "sha256:" + hex.EncodeToString(digest[:])
	return o, nil
}
