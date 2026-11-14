//go:build !governed

package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/evidence"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectcontext"
)

const maxProjectContextRefs = 256

// ProjectContextAudience is provided only by the already-authorized runtime.
// It is not a tool-input grant, nor a substitute for MemoryService authorizer
// or current task-memory authorization. SQL enforces its exact ACL/scope set.
type ProjectContextAudience struct {
	Actor    string
	ScopeIDs []string
	Worker   bool
}

type AcceptedProjectContract struct {
	Contract projectcontext.Contract
	Version  int64
	Digest   string
}

// ReadAcceptedProjectContract inspects immutable accepted state even when its
// required refs are stale. It returns NO qualification/admission/read grant.
func (s *Store) ReadAcceptedProjectContract(ctx context.Context, project projectcontext.ProjectBinding) (AcceptedProjectContract, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AcceptedProjectContract{}, err
	}
	defer tx.Rollback()
	c, version, digest, err := readProjectContract(ctx, tx, project)
	if err != nil {
		return AcceptedProjectContract{}, err
	}
	return AcceptedProjectContract{c, version, digest}, nil
}

type ProjectContractInventoryEntry struct {
	ID               string                `json:"id"`
	ProjectID        string                `json:"project_id"`
	Revision         int64                 `json:"revision"`
	ContentDigest    string                `json:"content_digest"`
	StateFingerprint string                `json:"state_fingerprint"`
	Kind             model.MemoryKind      `json:"kind"`
	Lifecycle        model.MemoryLifecycle `json:"lifecycle"`
	Authority        model.MemoryAuthority `json:"authority"`
	Scope            string                `json:"scope"`
	ScopeID          string                `json:"scope_id"`
}
type ProjectContractInventoryPage struct {
	Project            projectcontext.ProjectBinding   `json:"project"`
	CurrentVersion     int64                           `json:"current_version"`
	CurrentContract    *projectcontext.ContractRef     `json:"current_contract,omitempty"`
	Entries            []ProjectContractInventoryEntry `json:"entries"`
	CompletenessDigest string                          `json:"completeness_digest"`
	NextAfterID        string                          `json:"next_after_id,omitempty"`
}

// ReadProjectContractInventoryPage supplies trusted owner inspection metadata.
// Workers are refused because the completeness digest includes hidden IDs. Pages
// never qualify required coverage; acceptance independently checks completeness
// without paging/ranking. The transport still measures its complete wire cap.
func (s *Store) ReadProjectContractInventoryPage(ctx context.Context, project projectcontext.ProjectBinding, audience ProjectContextAudience, afterID string, limit int) (ProjectContractInventoryPage, error) {
	if project.CanonicalID == "" || project.StorageID == "" || audience.Actor == "" || len(audience.ScopeIDs) == 0 || audience.Worker {
		return ProjectContractInventoryPage{}, authz.ErrUnauthorized
	}
	if limit < 1 || limit > 8 {
		return ProjectContractInventoryPage{}, projectcontext.ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ProjectContractInventoryPage{}, err
	}
	defer tx.Rollback()
	ids, err := readProjectInventoryIDs(ctx, tx, project.StorageID)
	if err != nil {
		return ProjectContractInventoryPage{}, err
	}
	all := []model.MemoryRecordV2{}
	for _, id := range ids {
		all = append(all, model.MemoryRecordV2{ID: id, ProjectID: project.StorageID})
	}
	page := ProjectContractInventoryPage{Project: project, Entries: []ProjectContractInventoryEntry{}, CompletenessDigest: projectInventoryDigest(all)}
	current, version, digest, currentErr := readProjectContract(ctx, tx, project)
	if currentErr == nil {
		page.CurrentVersion = version
		page.CurrentContract = &projectcontext.ContractRef{ID: current.ID, Revision: current.Revision, Digest: digest}
	} else if !errors.Is(currentErr, projectcontext.ErrReviewPending) {
		return ProjectContractInventoryPage{}, currentErr
	}
	for _, id := range ids {
		if id <= afterID {
			continue
		}
		if err = authorizeProjectMemoryID(ctx, tx, project.StorageID, id, audience); errors.Is(err, authz.ErrUnauthorized) {
			continue
		} else if err != nil {
			return ProjectContractInventoryPage{}, err
		}
		if len(page.Entries) == limit {
			page.NextAfterID = page.Entries[len(page.Entries)-1].ID
			break
		}
		r, err := getMemoryV2(ctx, tx, project.StorageID, id)
		if err != nil {
			return ProjectContractInventoryPage{}, err
		}
		page.Entries = append(page.Entries, ProjectContractInventoryEntry{r.ID, r.ProjectID, r.Revision, r.ContentDigest, projectcontext.MemoryFingerprint(r), r.Kind, r.Lifecycle, r.Authority, r.Scope, r.ScopeID})
	}
	return page, nil
}

type ProjectContractSubmission struct {
	Action          string                  `json:"action"`
	ExpectedVersion int64                   `json:"expected_version"`
	Contract        projectcontext.Contract `json:"contract"`
}
type ProjectContractApprovalBinding struct {
	ProposalDigest  string `json:"proposal_digest"`
	ContractDigest  string `json:"contract_digest"`
	InventoryDigest string `json:"inventory_digest"`
	ExpectedVersion int64  `json:"expected_version"`
}
type ProjectContextSnapshot struct {
	Contract                                     projectcontext.Contract
	Version                                      int64
	ContractDigest, InventoryDigest, StateDigest string
	CurrentContract                              projectcontext.ContractRef
	TranslationDigest                            string
	Records                                      []model.MemoryRecordV2
}
type ProjectContextAdmission struct {
	ID, Stage, ParentReceiptID                string
	Audience                                  ProjectContextAudience
	Selection                                 projectcontext.Selection
	Binding                                   projectcontext.Binding
	ExpectedStateDigest, ExpectedPacketDigest string
	ByteCap                                   int
	Prefix                                    string
	Now                                       time.Time
	// Optional only for legacy store primitives. Runtime admission must supply
	// a freshly observed canonical authority snapshot; this is data, not a grant.
	ExpectedAuthority *ProjectContextAuthorityObservation
}

// ProjectContractInventory is an exact canonical accepted-source inventory,
// independent of Recall ranking/cache. All durable/verified operator/policy
// rows are review candidates regardless of Kind or Scope. Labels are NOT
// acceptance of their truth: unknown entries require explicit classification.
// Candidates/outcomes remain outside this inventory and are never promoted.
func (s *Store) ProjectContractInventory(ctx context.Context, project projectcontext.ProjectBinding, audience ProjectContextAudience) ([]model.MemoryRecordV2, string, error) {
	if audience.Worker {
		return nil, "", authz.ErrUnauthorized
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, "", err
	}
	defer tx.Rollback()
	records, err := readProjectContractInventory(ctx, tx, project, audience)
	if err != nil {
		return nil, "", err
	}
	return records, projectInventoryDigest(records), nil
}
func readProjectInventoryIDs(ctx context.Context, tx *sql.Tx, storage string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT memory_id FROM memory_records_v2 WHERE project_id=? AND lifecycle IN ('durable','verified') AND authority IN ('operator','policy') ORDER BY memory_id LIMIT ?`, storage, maxProjectContextRefs+1)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(ids) > maxProjectContextRefs {
		return nil, projectcontext.ErrBudget
	}
	return ids, nil
}
func authorizeProjectMemoryID(ctx context.Context, tx *sql.Tx, project, id string, audience ProjectContextAudience) error {
	if project == "" || id == "" || audience.Actor == "" || len(audience.ScopeIDs) == 0 {
		return authz.ErrUnauthorized
	}
	query := `SELECT EXISTS(SELECT 1 FROM memory_records_v2 WHERE project_id=? AND memory_id=? AND (acl_scope='' OR acl_scope=?) AND scope_id IN (`
	args := []any{project, id, audience.Actor}
	for i, scope := range audience.ScopeIDs {
		if i > 0 {
			query += ","
		}
		query += "?"
		args = append(args, scope)
	}
	query += ")"
	if audience.Worker {
		query += workerMemoryAudiencePredicate
	}
	query += ")"
	var allowed bool
	if err := tx.QueryRowContext(ctx, query, args...).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return authz.ErrUnauthorized
	}
	return nil
}
func readProjectContractInventory(ctx context.Context, tx *sql.Tx, project projectcontext.ProjectBinding, audience ProjectContextAudience) ([]model.MemoryRecordV2, error) {
	if project.CanonicalID == "" || project.StorageID == "" || audience.Actor == "" || len(audience.ScopeIDs) == 0 {
		return nil, authz.ErrUnauthorized
	}
	ids, err := readProjectInventoryIDs(ctx, tx, project.StorageID)
	if err != nil {
		return nil, err
	}
	result := make([]model.MemoryRecordV2, 0, len(ids))
	for _, id := range ids {
		if err = authorizeProjectMemoryID(ctx, tx, project.StorageID, id, audience); err != nil {
			return nil, err
		}
		r, err := getMemoryV2(ctx, tx, project.StorageID, id)
		if err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, nil
}

// Completeness binds canonical accepted IDs, not mutable record states. Known
// classifications remain inspectable if their row becomes inactive; their
// exact state matters only to applicable mandatory task context.
func projectInventoryDigest(records []model.MemoryRecordV2) string {
	ids := make([]string, 0, len(records))
	for _, r := range records {
		ids = append(ids, r.ProjectID+"\x00"+r.ID)
	}
	sort.Strings(ids)
	return projectcontext.Digest("marshal.project.accepted-inventory.v1", ids)
}
func projectRecordAuthorized(r model.MemoryRecordV2, a ProjectContextAudience) bool {
	if a.Actor == "" || (r.ACLScope != "" && r.ACLScope != a.Actor) {
		return false
	}
	scope := false
	for _, id := range a.ScopeIDs {
		if id == r.ScopeID {
			scope = true
		}
	}
	if !scope {
		return false
	}
	if a.Worker && (r.IsSessionHistory() || r.Scope == "session" || r.Source.Kind == "external" || r.Source.Kind == "shared_channel" || strings.HasPrefix(r.Source.Reference, "external:") || strings.HasPrefix(r.ID, "MEM-IMPORT-")) {
		return false
	}
	return true
}

// AdmitMCPProjectContract stores a bounded checked DRAFT in the existing
// proposal journal. Neither this method nor caller labels activate a contract.
func (s *Store) AdmitMCPProjectContract(ctx context.Context, source MCPProposal, sub ProjectContractSubmission) (MCPProposal, bool, error) {
	var err error
	sub.Contract, err = sub.Contract.Canonical()
	if err != nil {
		return MCPProposal{}, false, err
	}
	if sub.Action != "project-contract" || sub.ExpectedVersion < 0 || sub.Contract.Revision != sub.ExpectedVersion+1 || sub.Contract.ReviewedInventoryDigest == "" || len(sub.Contract.Required) > maxProjectContextRefs {
		return MCPProposal{}, false, projectcontext.ErrInvalid
	}
	if source.ID == "" || source.Launch == "" || source.Owner == "" || source.Provider == "" || source.Submission == "" {
		return MCPProposal{}, false, projectcontext.ErrInvalid
	}
	// A revision of the accepted contract can only be accepted when it names
	// what it supersedes and why; one that cannot be accepted is refused now,
	// not after the owner accepted it.
	if sub.ExpectedVersion > 0 && (sub.Contract.SupersedesDigest == "" || strings.TrimSpace(sub.Contract.RevisionReason) == "") {
		return MCPProposal{}, false, projectcontext.ErrInvalid
	}
	payload, err := json.Marshal(sub)
	if err != nil {
		return MCPProposal{}, false, err
	}
	if len(payload) > projectcontext.MaximumContractSubmissionBytes {
		return MCPProposal{}, false, projectcontext.ErrBudget
	}
	source.Payload = string(payload)
	return s.AdmitMCPProposal(ctx, sub.Contract.Project.StorageID, source)
}

func readProjectContract(ctx context.Context, tx *sql.Tx, project projectcontext.ProjectBinding) (projectcontext.Contract, int64, string, error) {
	var raw, digest, storage, state string
	var version int64
	err := tx.QueryRowContext(ctx, `SELECT r.contract_json,r.digest,a.storage_project_id,a.bootstrap_state,a.cas_version FROM extreme_project_contract_active a JOIN extreme_project_contract_revisions r ON r.project_id=a.project_id AND r.contract_id=a.contract_id AND r.revision=a.revision AND r.digest=a.digest WHERE a.project_id=?`, project.CanonicalID).Scan(&raw, &digest, &storage, &state, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return projectcontext.Contract{}, 0, "", projectcontext.ErrReviewPending
	}
	if err != nil {
		return projectcontext.Contract{}, 0, "", err
	}
	if storage != project.StorageID || state != "accepted" {
		return projectcontext.Contract{}, 0, "", projectcontext.Changed("project_context_extreme.go:326")
	}
	var c projectcontext.Contract
	if err = json.Unmarshal([]byte(raw), &c); err != nil {
		return c, 0, "", err
	}
	actual, err := c.CanonicalDigest()
	if err != nil || actual != digest || c.Project != project {
		return c, 0, "", projectcontext.Changed("project_context_extreme.go:334")
	}
	rows, err := tx.QueryContext(ctx, `SELECT ref_json FROM extreme_project_contract_required_refs WHERE project_id=? AND contract_id=? AND contract_revision=? ORDER BY memory_project_id,memory_id`, project.CanonicalID, c.ID, c.Revision)
	if err != nil {
		return c, 0, "", err
	}
	refs := []projectcontext.MemoryRef{}
	for rows.Next() {
		var body string
		if err = rows.Scan(&body); err != nil {
			rows.Close()
			return c, 0, "", err
		}
		var ref projectcontext.MemoryRef
		if err = json.Unmarshal([]byte(body), &ref); err != nil {
			rows.Close()
			return c, 0, "", err
		}
		refs = append(refs, ref)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return c, 0, "", err
	}
	expected := c.Required
	if expected == nil {
		expected = []projectcontext.MemoryRef{}
	}
	if !reflect.DeepEqual(refs, expected) {
		return c, 0, "", projectcontext.Changed("project_context_extreme.go:364")
	}
	return c, version, digest, nil
}

// CommitMCPProjectContractDecision is an INTERNAL effect primitive. The app/TUI
// must first authenticate current owner/lease and exact CommandPermission. The
// store additionally demands the original checked proposal AND a different
// applying decision in the existing authenticated request journal. No caller
// IsAccepted flag, raw contract write, or model-supplied receipt creates policy.
func (s *Store) CommitMCPProjectContractDecision(ctx context.Context, req permission.Request, allow bool, audience ProjectContextAudience, now time.Time, audit model.Event) (projectcontext.Contract, error) {
	return s.commitMCPProjectContractDecision(ctx, req, allow, audience, now, audit, false)
}

// CommitMCPProjectContractDecisionOnInstruction is the Marshal self-acceptance
// path: an allow additionally consumes, in the same transaction, the owner's
// recorded instruction bound to this exact contract, launch and version.
func (s *Store) CommitMCPProjectContractDecisionOnInstruction(ctx context.Context, req permission.Request, allow bool, audience ProjectContextAudience, now time.Time, audit model.Event) (projectcontext.Contract, error) {
	return s.commitMCPProjectContractDecision(ctx, req, allow, audience, now, audit, true)
}

func (s *Store) commitMCPProjectContractDecision(ctx context.Context, req permission.Request, allow bool, audience ProjectContextAudience, now time.Time, audit model.Event, ownerInstruction bool) (projectcontext.Contract, error) {
	if audience.Worker {
		return projectcontext.Contract{}, authz.ErrUnauthorized
	}
	if req.Kind != "project-contract" || req.ProposalID == "" || req.SourceOccurrence == "" || req.SourceOccurrence == req.ProposalID || req.SourceLaunch == "" || now.IsZero() {
		return projectcontext.Contract{}, projectcontext.ErrInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return projectcontext.Contract{}, err
	}
	defer tx.Rollback()
	// Obtain writer position before reading authority/canonical memory. This
	// takes SQLite's write reservation without mutating accepted project state.
	if _, err = tx.ExecContext(ctx, `UPDATE extreme_mcp_proposals SET state=state WHERE project_id=? AND occurrence_id=?`, req.Scope, req.SourceOccurrence); err != nil {
		return projectcontext.Contract{}, err
	}
	original, err := readMCPProposal(ctx, tx, req.Scope, req.ProposalID)
	if err != nil {
		return projectcontext.Contract{}, err
	}
	decision, err := readMCPProposal(ctx, tx, req.Scope, req.SourceOccurrence)
	if err != nil {
		return projectcontext.Contract{}, err
	}
	var sub ProjectContractSubmission
	if err = json.Unmarshal([]byte(original.Payload), &sub); err != nil {
		return projectcontext.Contract{}, err
	}
	normalized, err := sub.Contract.Canonical()
	if err != nil {
		return projectcontext.Contract{}, err
	}
	sub.Contract = normalized
	exactPayload, _ := json.Marshal(sub)
	if string(exactPayload) != original.Payload || original.Digest != mcpPayloadDigest(original.Payload) || sub.Action != "project-contract" || sub.Contract.Project.StorageID != req.Scope || req.Object != sub.Contract.ID {
		return projectcontext.Contract{}, projectcontext.Changed("project_context_extreme.go:421")
	}
	contractDigest, err := sub.Contract.CanonicalDigest()
	if err != nil {
		return projectcontext.Contract{}, err
	}
	bound := ProjectContractApprovalBinding{original.Digest, contractDigest, sub.Contract.ReviewedInventoryDigest, sub.ExpectedVersion}
	bindingJSON, _ := json.Marshal(bound)
	if req.ApprovalBinding != string(bindingJSON) || original.Launch != req.SourceLaunch || decision.Launch != req.SourceLaunch || original.Owner != decision.Owner || original.Provider != decision.Provider || original.State != "admitted" || decision.State != "applying" || audience.Actor != decision.Owner {
		return projectcontext.Contract{}, projectcontext.Changed("project_context_extreme.go:430")
	}
	var requestJSON, requestKey, outcome string
	if err = tx.QueryRowContext(ctx, `SELECT request_json,request_key,outcome FROM marshal_proposals WHERE project_id=? AND occurrence_id=?`, req.Scope, decision.ID).Scan(&requestJSON, &requestKey, &outcome); err != nil {
		return projectcontext.Contract{}, err
	}
	expectedRequest, _ := json.Marshal(req)
	if requestJSON != string(expectedRequest) || requestKey != req.Key() || outcome != "" {
		return projectcontext.Contract{}, projectcontext.Changed("project_context_extreme.go:438")
	}
	var answer struct {
		Action string `json:"action"`
		ID     string `json:"id"`
		Allow  bool   `json:"allow"`
		Reason string `json:"reason"`
	}
	if err = decodeProjectDecision([]byte(decision.Payload), &answer); err != nil || answer.Action != "decide" || answer.ID == "" || strings.TrimSpace(answer.Reason) == "" || answer.Allow != allow || decision.Digest != mcpPayloadDigest(decision.Payload) {
		return projectcontext.Contract{}, projectcontext.Changed("project_context_extreme.go:447")
	}
	if audit.ProjectID != req.Scope || audit.ID == "" || audit.Type != "PERMISSION_DECIDED" {
		return projectcontext.Contract{}, projectcontext.ErrInvalid
	}
	if !allow {
		if err = commitProjectDecisionJournal(ctx, s, tx, original, decision, "denied", audit); err != nil {
			return projectcontext.Contract{}, err
		}
		return projectcontext.Contract{}, tx.Commit()
	}
	inventory, err := readProjectContractInventory(ctx, tx, sub.Contract.Project, audience)
	if err != nil {
		return projectcontext.Contract{}, err
	}
	if projectInventoryDigest(inventory) != sub.Contract.ReviewedInventoryDigest {
		return projectcontext.Contract{}, projectcontext.ErrReviewPending
	}
	if err = validateProjectContractReferences(ctx, tx, sub.Contract, inventory, audience, now); err != nil {
		return projectcontext.Contract{}, err
	}
	var currentVersion int64
	var currentID, currentDigest string
	err = tx.QueryRowContext(ctx, `SELECT cas_version,coalesce(contract_id,''),coalesce(digest,'') FROM extreme_project_contract_active WHERE project_id=?`, sub.Contract.Project.CanonicalID).Scan(&currentVersion, &currentID, &currentDigest)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return projectcontext.Contract{}, err
	}
	if currentVersion != sub.ExpectedVersion || sub.Contract.Revision != currentVersion+1 || (currentVersion > 0 && (currentID != sub.Contract.ID || sub.Contract.SupersedesDigest != currentDigest || strings.TrimSpace(sub.Contract.RevisionReason) == "")) {
		return projectcontext.Contract{}, model.ErrConflict
	}
	if ownerInstruction {
		if _, err = consumeProjectContractInstructionTx(ctx, tx, req.Scope, req.SourceLaunch, decision.ID, sub, contractDigest); err != nil {
			return projectcontext.Contract{}, err
		}
	}
	contractJSON, _ := json.Marshal(sub.Contract)
	if _, err = tx.ExecContext(ctx, `INSERT INTO extreme_project_contract_revisions(project_id,contract_id,revision,digest,contract_json,acceptance_occurrence,created_at) VALUES(?,?,?,?,?,?,?)`, sub.Contract.Project.CanonicalID, sub.Contract.ID, sub.Contract.Revision, contractDigest, string(contractJSON), decision.ID, now.UTC().Format(time.RFC3339Nano)); err != nil {
		return projectcontext.Contract{}, err
	}
	for _, ref := range sub.Contract.Required {
		raw, _ := json.Marshal(ref)
		if _, err = tx.ExecContext(ctx, `INSERT INTO extreme_project_contract_required_refs(project_id,contract_id,contract_revision,memory_project_id,memory_id,memory_revision,content_digest,class,ref_json) VALUES(?,?,?,?,?,?,?,?,?)`, sub.Contract.Project.CanonicalID, sub.Contract.ID, sub.Contract.Revision, ref.ProjectID, ref.ID, ref.Revision, ref.ContentDigest, ref.Class, string(raw)); err != nil {
			return projectcontext.Contract{}, err
		}
	}
	activeResult, activeErr := tx.ExecContext(ctx, `INSERT INTO extreme_project_contract_active(project_id,storage_project_id,contract_id,revision,digest,cas_version,bootstrap_state) VALUES(?,?,?,?,?,?,'accepted') ON CONFLICT(project_id) DO UPDATE SET contract_id=excluded.contract_id,revision=excluded.revision,digest=excluded.digest,cas_version=excluded.cas_version,bootstrap_state='accepted' WHERE extreme_project_contract_active.cas_version=? AND extreme_project_contract_active.storage_project_id=excluded.storage_project_id`, sub.Contract.Project.CanonicalID, sub.Contract.Project.StorageID, sub.Contract.ID, sub.Contract.Revision, contractDigest, sub.Contract.Revision, sub.ExpectedVersion)
	if activeErr != nil {
		return projectcontext.Contract{}, activeErr
	}
	activeCount, activeErr := activeResult.RowsAffected()
	if activeErr != nil || activeCount != 1 {
		return projectcontext.Contract{}, model.ErrConflict
	}
	if err = commitProjectDecisionJournal(ctx, s, tx, original, decision, "committed", audit); err != nil {
		return projectcontext.Contract{}, err
	}
	if err = tx.Commit(); err != nil {
		return projectcontext.Contract{}, err
	}
	return sub.Contract, nil
}
func mcpPayloadDigest(payload string) string {
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

// A retained candidate is never made required by a class label. Normative refs
// need a separately supported durable/verified operator/policy lifecycle;
// empirical refs still need current evidence and exact state validation.
func validateProjectContractReferences(ctx context.Context, tx *sql.Tx, c projectcontext.Contract, inventory []model.MemoryRecordV2, audience ProjectContextAudience, now time.Time) error {
	byID := map[string]projectcontext.MemoryRef{}
	for _, ref := range c.Required {
		byID[ref.ID] = ref
	}
	for _, r := range inventory {
		ref, ok := byID[r.ID]
		if !ok {
			return projectcontext.RequiredMemory(projectcontext.ErrReviewPending, r.ID, "is listed by marshal_project_contract_inventory but missing from required; read every inventory page and classify each entry in required")
		}
		if ref.Revision != r.Revision || ref.StateFingerprint != projectcontext.MemoryFingerprint(r) {
			return projectcontext.RequiredMemory(projectcontext.ErrReviewPending, r.ID, "changed after the inventory was read; read marshal_project_contract_inventory again and submit with its current revision and state_fingerprint")
		}
	}
	// A memory the Marshal saved itself is never owner authority: it cannot
	// bind as a rule. Naming it tells the Marshal how the draft can pass.
	notOwnerConfirmed := func(ref projectcontext.MemoryRef) error {
		return projectcontext.RequiredMemory(projectcontext.ErrReviewPending, ref.ID, "was saved by the Marshal and not confirmed by the owner, so it cannot be required as "+ref.Class+"; remove it from required and state the rule in contract.invariants instead")
	}
	for _, ref := range c.Required {
		if err := authorizeProjectMemoryID(ctx, tx, ref.ProjectID, ref.ID, audience); err != nil {
			return err
		}
		r, err := getMemoryV2(ctx, tx, ref.ProjectID, ref.ID)
		if err != nil {
			return projectcontext.RequiredMemory(projectcontext.ErrMissing, ref.ID, "was not found; remove it from required")
		}
		if !projectRecordAuthorized(r, audience) {
			return authz.ErrUnauthorized
		}
		if ref.Class == projectcontext.ContextOnly {
			if !projectcontext.EligibleAcceptedRecord(r) {
				return notOwnerConfirmed(ref)
			}
			if r.Scope != ref.Scope || r.ScopeID != ref.ScopeID || r.Revision != ref.Revision || r.ContentDigest != ref.ContentDigest || projectcontext.MemoryFingerprint(r) != ref.StateFingerprint {
				return projectcontext.ErrReviewPending
			}
			continue
		}
		if err = projectcontext.ValidateRequiredRecord(r, ref, now); err != nil {
			if err == projectcontext.ErrReviewPending && !projectcontext.EligibleAcceptedRecord(r) {
				return notOwnerConfirmed(ref)
			}
			return err
		}
		if err = validateProjectEvidence(ctx, tx, r, ref); err != nil {
			return err
		}
	}
	return nil
}
func validateProjectEvidence(ctx context.Context, tx *sql.Tx, r model.MemoryRecordV2, ref projectcontext.MemoryRef) error {
	if !ref.EvidenceRequired {
		return nil
	}
	for _, id := range r.EvidenceIDs {
		var typ, state, digest, raw string
		if err := tx.QueryRowContext(ctx, `SELECT node_type,state,digest,metadata_json FROM evidence_nodes WHERE node_id=?`, id).Scan(&typ, &state, &digest, &raw); err != nil {
			return projectcontext.Changed("project_context_extreme.go:560")
		}
		if state != string(evidence.StateStored) && state != string(evidence.StateLinked) {
			return projectcontext.Changed("project_context_extreme.go:563")
		}
		var metadata map[string]string
		if json.Unmarshal([]byte(raw), &metadata) != nil {
			return projectcontext.Changed("project_context_extreme.go:567")
		}
		actual, err := evidence.CanonicalDigest(evidence.NodeType(typ), metadata)
		if err != nil || actual != digest {
			return projectcontext.Changed("project_context_extreme.go:571")
		}
	}
	return nil
}
func commitProjectDecisionJournal(ctx context.Context, s *Store, tx *sql.Tx, original, decision MCPProposal, state string, audit model.Event) error {
	if err := s.AppendEvent(ctx, tx, audit); err != nil {
		return err
	}
	for _, p := range []MCPProposal{original, decision} {
		if _, err := tx.ExecContext(ctx, `UPDATE extreme_mcp_proposals SET state=?,reason=? WHERE project_id=? AND occurrence_id=?`, state, "project contract decision committed", audit.ProjectID, p.ID); err != nil {
			return err
		}
		if err := s.mcpAudit(ctx, tx, audit.ProjectID, p.ID, p.Launch, state, "project contract decision committed"); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE marshal_proposals SET outcome=? WHERE project_id=? AND occurrence_id=? AND outcome=''`, state, audit.ProjectID, p.ID); err != nil {
			return err
		}
	}
	return nil
}

// ReadProjectContextSnapshot is a consistent exact canonical read, never an
// admission or authorization grant. Missing/changed essential context refuses.
func (s *Store) ReadProjectContextSnapshot(ctx context.Context, project projectcontext.ProjectBinding, audience ProjectContextAudience, selection projectcontext.Selection, now time.Time) (ProjectContextSnapshot, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ProjectContextSnapshot{}, err
	}
	defer tx.Rollback()
	return readProjectContextSnapshot(ctx, tx, project, audience, selection, now)
}
func readProjectContextSnapshot(ctx context.Context, tx *sql.Tx, project projectcontext.ProjectBinding, audience ProjectContextAudience, selection projectcontext.Selection, now time.Time) (ProjectContextSnapshot, error) {
	c, version, digest, err := readProjectContract(ctx, tx, project)
	if err != nil {
		return ProjectContextSnapshot{}, err
	}
	if err = validateProjectClassifications(ctx, tx, c, audience, selection); err != nil {
		return ProjectContextSnapshot{}, err
	}
	inventoryDigest := c.ReviewedInventoryDigest
	records := []model.MemoryRecordV2{}
	for _, ref := range c.Required {
		if !ref.Mandatory() || !ref.Applies.Matches(selection) {
			continue
		}
		if err := authorizeProjectMemoryID(ctx, tx, ref.ProjectID, ref.ID, audience); err != nil {
			return ProjectContextSnapshot{}, err
		}
		r, err := getMemoryV2(ctx, tx, ref.ProjectID, ref.ID)
		if err != nil {
			return ProjectContextSnapshot{}, projectcontext.ErrMissing
		}
		if !projectRecordAuthorized(r, audience) {
			return ProjectContextSnapshot{}, authz.ErrUnauthorized
		}
		if err = projectcontext.ValidateRequiredRecord(r, ref, now); err != nil {
			return ProjectContextSnapshot{}, err
		}
		if err = validateProjectEvidence(ctx, tx, r, ref); err != nil {
			return ProjectContextSnapshot{}, err
		}
		records = append(records, r)
	}
	evidenceFingerprints := []string{}
	for _, ref := range c.Required {
		if !ref.Mandatory() || !ref.Applies.Matches(selection) || !ref.EvidenceRequired {
			continue
		}
		for _, r := range records {
			if r.ID == ref.ID {
				fp, err := projectEvidenceFingerprint(ctx, tx, r)
				if err != nil {
					return ProjectContextSnapshot{}, err
				}
				evidenceFingerprints = append(evidenceFingerprints, fp)
			}
		}
	}
	loaded := []projectcontext.LoadedReference{}
	for _, r := range records {
		loaded = append(loaded, projectcontext.LoadedReference{ID: r.ID, ProjectID: r.ProjectID, Revision: r.Revision, ContentDigest: r.ContentDigest, StateFingerprint: projectcontext.MemoryFingerprint(r)})
	}
	applicableDigest, err := projectcontext.ApplicableContractDigest(c, selection)
	if err != nil {
		return ProjectContextSnapshot{}, err
	}
	stateDigest := projectcontext.Digest("marshal.project.required-state.v1", struct {
		Contract, Inventory, Actor string
		Scopes                     []string
		Worker                     bool
		Loaded                     []projectcontext.LoadedReference
		Evidence                   []string
	}{applicableDigest, "classified-applicable", audience.Actor, sortedProjectScopes(audience.ScopeIDs), audience.Worker, loaded, evidenceFingerprints})
	return ProjectContextSnapshot{Contract: c, Version: version, ContractDigest: digest, InventoryDigest: inventoryDigest, StateDigest: stateDigest, Records: records, CurrentContract: projectcontext.ContractRef{ID: c.ID, Revision: c.Revision, Digest: digest}}, nil
}
func sortedProjectScopes(scopes []string) []string {
	out := append([]string(nil), scopes...)
	sort.Strings(out)
	return out
}

// CommitProjectContextAdmission atomically REVALIDATES current canonical state
// and records exact coverage. It does not consume an approval, authorize a model,
// acquire lifecycle ownership, or invoke a provider. Later runtime integration
// must bind its exact result to an opaque one-shot invocation; a receipt alone
// is NEVER an execution grant. No SQL transaction spans a network/model call.
func (s *Store) CommitProjectContextAdmission(ctx context.Context, a ProjectContextAdmission) (projectcontext.Receipt, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return projectcontext.Receipt{}, err
	}
	defer tx.Rollback()
	receipt, err := s.commitProjectContextAdmissionTx(ctx, tx, a)
	if err != nil {
		return projectcontext.Receipt{}, err
	}
	if err = tx.Commit(); err != nil {
		return projectcontext.Receipt{}, err
	}
	return receipt, nil
}

// commitProjectContextAdmissionTx permits completion coverage and canonical
// attestation to share one transaction. Callers must not use s.db inside tx.
func (s *Store) commitProjectContextAdmissionTx(ctx context.Context, tx *sql.Tx, a ProjectContextAdmission) (projectcontext.Receipt, error) {
	if a.ID == "" || a.Stage == "" || a.Now.IsZero() || a.ExpectedStateDigest == "" || a.Binding.RequiredStateDigest != a.ExpectedStateDigest || a.ExpectedPacketDigest == "" || a.Binding.Principal != a.Audience.Actor || a.Binding.GoalID == "" || a.Binding.GoalRevision < 1 || a.Binding.Session == "" {
		return projectcontext.Receipt{}, projectcontext.ErrInvalid
	}
	if a.Selection.TaskID != a.Binding.TaskID || a.Selection.CanonicalTaskID != a.Binding.CanonicalTaskID {
		return projectcontext.Receipt{}, projectcontext.Changed("project_context_extreme.go:702")
	}
	switch a.Stage {
	case "planning", "handoff", "admission", "dispatch", "resume", "native_resume", "retry", "fallback", "review", "completion":
	default:
		return projectcontext.Receipt{}, projectcontext.ErrInvalid
	}
	result, err := tx.ExecContext(ctx, `UPDATE extreme_project_contract_active SET cas_version=cas_version WHERE project_id=? AND bootstrap_state='accepted'`, a.Binding.Project.CanonicalID)
	if err != nil {
		return projectcontext.Receipt{}, err
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		return projectcontext.Receipt{}, projectcontext.Changed("project_context_extreme.go:715")
	}
	var currentAuthority *ProjectContextAuthorityObservation
	if expected := a.ExpectedAuthority; expected != nil {
		if expected.CheckedAt.IsZero() || expected.CheckedAt.After(a.Now) || expected.Digest == "" || normalizeProjectAuthorityBinding(expected.Request.Binding) != normalizeProjectAuthorityBinding(a.Binding) {
			return projectcontext.Receipt{}, projectcontext.Changed("project_context_extreme.go:720")
		}
		current, authorityErr := readProjectContextAuthorityTx(ctx, tx, expected.Request, a.Now)
		if authorityErr != nil {
			return projectcontext.Receipt{}, authorityErr
		}
		if current.Digest != expected.Digest {
			return projectcontext.Receipt{}, projectcontext.Changed("project_context_extreme.go:727")
		}
		currentAuthority = &current
	}
	snapshot, err := readProjectContextSnapshotBound(ctx, tx, a.Binding.Project, a.Audience, a.Selection, a.Now, a.Binding.Contract)
	if err != nil {
		return projectcontext.Receipt{}, err
	}
	if snapshot.StateDigest != a.ExpectedStateDigest {
		return projectcontext.Receipt{}, projectcontext.Changed("project_context_extreme.go:736")
	}
	var goalID string
	var revision int64
	if err = tx.QueryRowContext(ctx, `SELECT active_goal_id,active_revision FROM goal_active WHERE session_id=?`, a.Binding.Session).Scan(&goalID, &revision); err != nil || goalID != a.Binding.GoalID || revision != a.Binding.GoalRevision {
		return projectcontext.Receipt{}, projectcontext.Changed("project_context_extreme.go:741")
	}
	goal, err := readGoalContract(ctx, tx, goalID, revision)
	if err != nil {
		return projectcontext.Receipt{}, err
	}
	if goal.ProjectID != a.Binding.Project.CanonicalID && goal.ProjectID != a.Binding.Project.StorageID {
		return projectcontext.Receipt{}, projectcontext.Changed("project_context_extreme.go:748")
	}
	if a.Stage != "planning" {
		if err = validateProjectContextPlan(ctx, tx, a.Binding); err != nil {
			return projectcontext.Receipt{}, err
		}
	}
	packet, err := projectcontext.RenderWithPrefix(projectcontext.Manifest{Contract: snapshot.Contract, Goal: projectcontext.GoalFromCanonical(goal), Binding: a.Binding, Selection: a.Selection, Records: snapshot.Records}, a.Prefix, a.ByteCap)
	if err != nil {
		return projectcontext.Receipt{}, err
	}
	if packet.Digest != a.ExpectedPacketDigest {
		return projectcontext.Receipt{}, projectcontext.Changed("project_context_extreme.go:760")
	}
	if a.ParentReceiptID != "" {
		var raw string
		if err = tx.QueryRowContext(ctx, `SELECT receipt_json FROM extreme_project_context_receipts WHERE receipt_id=? AND project_id=?`, a.ParentReceiptID, a.Binding.Project.CanonicalID).Scan(&raw); err != nil {
			return projectcontext.Receipt{}, projectcontext.Changed("project_context_extreme.go:765")
		}
		var parent projectcontext.Receipt
		if json.Unmarshal([]byte(raw), &parent) != nil || parent.Binding.Contract != a.Binding.Contract || parent.Binding.RunID != a.Binding.RunID || parent.Binding.TaskID != a.Binding.TaskID || parent.Binding.CanonicalTaskID != a.Binding.CanonicalTaskID || parent.Binding.Principal != a.Binding.Principal || parent.Binding.Session != a.Binding.Session || parent.Binding.GoalID != a.Binding.GoalID || parent.Binding.GoalRevision != a.Binding.GoalRevision || parent.Outcome != "QUALIFIED" {
			return projectcontext.Receipt{}, projectcontext.Changed("project_context_extreme.go:769")
		}
	}
	binding := a.Binding
	binding.FormatVersion = projectcontext.FormatVersion
	binding.InventoryDigest = snapshot.InventoryDigest
	binding.ManifestDigest = packet.ManifestDigest
	receipt := projectcontext.Receipt{CurrentContract: snapshot.CurrentContract, TranslationDigest: snapshot.TranslationDigest, ID: a.ID, Stage: a.Stage, ParentReceiptID: a.ParentReceiptID, Binding: binding, PacketDigest: packet.Digest, Loaded: packet.Loaded, RequiredCount: len(packet.Loaded), IncludedCount: len(packet.Loaded), RenderedBytes: packet.Bytes, CheckedAt: a.Now.UTC(), Outcome: "QUALIFIED"}
	var validUntil *time.Time
	if currentAuthority != nil && currentAuthority.ValidUntil != nil {
		copy := *currentAuthority.ValidUntil
		validUntil = &copy
	}
	for _, ref := range snapshot.Contract.Required {
		if !ref.Mandatory() || !ref.Applies.Matches(a.Selection) {
			continue
		}
		for _, r := range snapshot.Records {
			if r.ID != ref.ID {
				continue
			}
			next := r.ValidTo
			if ref.MaxVerificationAgeSeconds > 0 && r.LastVerifiedAt != nil {
				until := r.LastVerifiedAt.Add(time.Duration(ref.MaxVerificationAgeSeconds) * time.Second)
				if next == nil || until.Before(*next) {
					next = &until
				}
			}
			if next != nil && (validUntil == nil || next.Before(*validUntil)) {
				copy := *next
				validUntil = &copy
			}
		}
	}
	receipt.ValidUntil = validUntil
	raw, _ := json.Marshal(receipt)
	bindingJSON, _ := json.Marshal(binding)
	var expiry any
	if validUntil != nil {
		expiry = validUntil.UTC().Format(time.RFC3339Nano)
	}
	var parentID any
	if a.ParentReceiptID != "" {
		parentID = a.ParentReceiptID
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO extreme_project_context_receipts(receipt_id,project_id,contract_id,contract_revision,stage,parent_receipt_id,manifest_digest,packet_digest,binding_json,receipt_json,outcome,required_count,included_count,rendered_bytes,created_at,valid_until) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, receipt.ID, binding.Project.CanonicalID, snapshot.CurrentContract.ID, snapshot.CurrentContract.Revision, receipt.Stage, parentID, binding.ManifestDigest, receipt.PacketDigest, string(bindingJSON), string(raw), receipt.Outcome, receipt.RequiredCount, receipt.IncludedCount, receipt.RenderedBytes, receipt.CheckedAt.Format(time.RFC3339Nano), expiry); err != nil {
		return projectcontext.Receipt{}, err
	}
	eventData := map[string]any{"receipt_id": receipt.ID, "stage": receipt.Stage, "manifest_digest": binding.ManifestDigest, "packet_digest": receipt.PacketDigest, "loaded_count": receipt.IncludedCount}
	if currentAuthority != nil {
		eventData["authority_digest"] = currentAuthority.Digest
		eventData["authority_purpose"] = currentAuthority.Request.Purpose
	}
	if err = s.AppendEvent(ctx, tx, model.Event{ID: "context-" + receipt.ID, ProjectID: binding.Project.StorageID, Type: "PROJECT_CONTEXT_QUALIFIED", Timestamp: receipt.CheckedAt, Data: eventData}); err != nil {
		return projectcontext.Receipt{}, err
	}
	return receipt, nil
}
func validateProjectContextPlan(ctx context.Context, tx *sql.Tx, b projectcontext.Binding) error {
	if b.PlanID == "" || b.PlanVersion < 1 || b.ApprovalScopeDigest == "" {
		return projectcontext.Changed("project_context_extreme.go:829")
	}
	var id, raw string
	var version int64
	if err := tx.QueryRowContext(ctx, `SELECT plan_id,version FROM execution_plan_active WHERE project_id=?`, b.Project.CanonicalID).Scan(&id, &version); err != nil || id != b.PlanID || version != b.PlanVersion {
		return projectcontext.Changed("project_context_extreme.go:834")
	}
	if err := tx.QueryRowContext(ctx, `SELECT plan_json FROM execution_plans WHERE plan_id=? AND version=?`, id, version).Scan(&raw); err != nil {
		return err
	}
	p, err := decodePlan(raw)
	if err != nil || p.State != plan.StateApproved || p.Goal.GoalID != b.GoalID || p.Goal.Revision != b.GoalRevision || p.ApprovalScopeDigest != b.ApprovalScopeDigest || p.ApprovedScope == nil || p.ApprovedScope.Digest() != b.ApprovalScopeDigest {
		return projectcontext.Changed("project_context_extreme.go:841")
	}
	// Future plan binding fields must also match this contract/manifest. Legacy
	// plan history decodes here, but this foundational primitive grants no turn.
	return nil
}

func decodeProjectDecision(raw []byte, dst any) error {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || len(fields) != 4 || fields["action"] == nil || fields["id"] == nil || fields["reason"] == nil {
		return projectcontext.ErrInvalid
	}
	answer := string(bytes.TrimSpace(fields["allow"]))
	if answer != "true" && answer != "false" {
		return projectcontext.ErrInvalid
	}

	// Exact JSON token traversal rejects duplicate keys at every nesting level;
	// the standard decoder alone otherwise silently takes the last duplicate.
	d := json.NewDecoder(bytes.NewReader(raw))
	if err := checkProjectJSONValue(d); err != nil {
		return err
	}
	if _, err := d.Token(); !errors.Is(err, io.EOF) {
		return projectcontext.ErrInvalid
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	return d.Decode(dst)
}
func checkProjectJSONValue(d *json.Decoder) error {
	token, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return err
			}
			s, ok := key.(string)
			if !ok || seen[s] {
				return projectcontext.ErrInvalid
			}
			seen[s] = true
			if err = checkProjectJSONValue(d); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if err := checkProjectJSONValue(d); err != nil {
				return err
			}
		}
	default:
		return projectcontext.ErrInvalid
	}
	_, err = d.Token()
	return err
}

func projectEvidenceFingerprint(ctx context.Context, tx *sql.Tx, r model.MemoryRecordV2) (string, error) {
	proofs := []string{}
	for _, id := range r.EvidenceIDs {
		var typ, state, digest string
		if err := tx.QueryRowContext(ctx, `SELECT node_type,state,digest FROM evidence_nodes WHERE node_id=?`, id).Scan(&typ, &state, &digest); err != nil {
			return "", projectcontext.Changed("project_context_extreme.go:915")
		}
		proofs = append(proofs, projectcontext.Digest("marshal.project.current-evidence.v1", []string{id, typ, state, digest}))
	}
	sort.Strings(proofs)
	return projectcontext.Digest("marshal.project.current-evidence-set.v1", proofs), nil
}

func validateProjectClassifications(ctx context.Context, tx *sql.Tx, c projectcontext.Contract, audience ProjectContextAudience, selection projectcontext.Selection) error {
	refs := map[string]projectcontext.MemoryRef{}
	for _, ref := range c.Required {
		refs[ref.ID] = ref
	}
	rows, err := tx.QueryContext(ctx, `SELECT memory_id,content_digest,scope,scope_id FROM memory_records_v2 WHERE project_id=? AND lifecycle IN ('durable','verified') AND authority IN ('operator','policy') ORDER BY memory_id LIMIT ?`, c.Project.StorageID, maxProjectContextRefs+1)
	if err != nil {
		return err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
		if count > maxProjectContextRefs {
			return projectcontext.ErrBudget
		}
		var id, content, scope, scopeID string
		if err = rows.Scan(&id, &content, &scope, &scopeID); err != nil {
			return err
		}
		ref, known := refs[id]
		if !known {
			return projectcontext.ErrReviewPending
		}
		if content == ref.ContentDigest && scope == ref.Scope && scopeID == ref.ScopeID {
			continue
		}
		currentRelevant := scope != "task" && scope != "operator_private" && scope != "agent" || scope == "task" && (scopeID == selection.TaskID || scopeID == selection.CanonicalTaskID) || ((scope == "operator_private" || scope == "agent") && scopeID == audience.Actor)
		if (ref.Mandatory() && ref.Applies.Matches(selection)) || currentRelevant && (ref.Class == projectcontext.ContextOnly || scope != ref.Scope || scopeID != ref.ScopeID) {
			return projectcontext.ErrReviewPending
		}
	}
	return rows.Err()
}

// ReadProjectContextSnapshotBound validates current accepted authority and can
// translate an older genuinely accepted review only if architecture plus ALL
// applicable mandatory refs are exactly unchanged. This permits beta-only
// revisions without silently approving changed alpha/architecture scope.
func (s *Store) ReadProjectContextSnapshotBound(ctx context.Context, project projectcontext.ProjectBinding, audience ProjectContextAudience, selection projectcontext.Selection, now time.Time, reviewed projectcontext.ContractRef) (ProjectContextSnapshot, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ProjectContextSnapshot{}, err
	}
	defer tx.Rollback()
	return readProjectContextSnapshotBound(ctx, tx, project, audience, selection, now, reviewed)
}
func readProjectContextSnapshotBound(ctx context.Context, tx *sql.Tx, project projectcontext.ProjectBinding, audience ProjectContextAudience, selection projectcontext.Selection, now time.Time, reviewed projectcontext.ContractRef) (ProjectContextSnapshot, error) {
	current, err := readProjectContextSnapshot(ctx, tx, project, audience, selection, now)
	if err != nil {
		return ProjectContextSnapshot{}, err
	}
	if reviewed == current.CurrentContract {
		return current, nil
	}
	var raw, digest string
	if err = tx.QueryRowContext(ctx, `SELECT contract_json,digest FROM extreme_project_contract_revisions WHERE project_id=? AND contract_id=? AND revision=? AND digest=?`, project.CanonicalID, reviewed.ID, reviewed.Revision, reviewed.Digest).Scan(&raw, &digest); err != nil {
		return ProjectContextSnapshot{}, projectcontext.Changed("project_context_extreme.go:980")
	}
	var old projectcontext.Contract
	if json.Unmarshal([]byte(raw), &old) != nil || old.Project != project {
		return ProjectContextSnapshot{}, projectcontext.Changed("project_context_extreme.go:984")
	}
	actual, err := old.CanonicalDigest()
	if err != nil || actual != digest {
		return ProjectContextSnapshot{}, projectcontext.Changed("project_context_extreme.go:988")
	}
	before, err := projectcontext.ApplicableContractDigest(old, selection)
	if err != nil {
		return ProjectContextSnapshot{}, err
	}
	after, err := projectcontext.ApplicableContractDigest(current.Contract, selection)
	if err != nil || before != after {
		return ProjectContextSnapshot{}, projectcontext.Changed("project_context_extreme.go:996")
	}
	current.TranslationDigest = projectcontext.Digest("marshal.project.accepted-translation.v1", struct {
		Reviewed, Current projectcontext.ContractRef
		Applicable        string
	}{reviewed, current.CurrentContract, after})
	current.Contract = old
	current.ContractDigest = reviewed.Digest
	return current, nil
}
