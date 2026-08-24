package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/memory/security"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

type ConsolidationMode string

const (
	ConsolidateProcedure    ConsolidationMode = "procedure"
	ConsolidateAntiPattern  ConsolidationMode = "anti_pattern"
	ConsolidateVerifiedFact ConsolidationMode = "verified_fact"

	minConsolidationSources  = 2
	maxConsolidationSources  = 32
	maxConsolidationEvidence = 512
)

type ConsolidationRequest struct {
	ProjectID       string                `json:"project_id"`
	Mode            ConsolidationMode     `json:"mode"`
	Subject         string                `json:"subject,omitempty"`
	Scope           model.MemoryScopeKind `json:"scope"`
	ScopeID         string                `json:"scope_id"`
	SourceMemoryIDs []string              `json:"source_memory_ids"`
}

type ConsolidationResult struct {
	Candidate model.MemoryRecordV2 `json:"candidate"`
	Existing  bool                 `json:"existing"`
}

// ProposeOutcomeConsolidation is the bounded run-completion scheduler. It does
// nothing until two related canonical episodes exist, then delegates to the
// same governed candidate-only path used by explicit maintenance.
func (s *MemoryService) ProposeOutcomeConsolidation(ctx context.Context, principal authz.Principal, outcome model.MemoryRecordV2, subject string) (*ConsolidationResult, error) {
	if outcome.Scope != string(model.ScopeTask) || outcome.ScopeID == "" || outcome.RunID == "" {
		return nil, nil
	}
	records, err := s.store.ListMemoryV2(ctx, store.MemoryQueryFilter{
		ProjectID: outcome.ProjectID, Scope: model.ScopeTask, ScopeID: outcome.ScopeID, ActorID: principal.ID, Limit: maxConsolidationSources * 4,
	})
	if err != nil {
		return nil, err
	}
	sourceIDs := make([]string, 0, maxConsolidationSources)
	mode := ConsolidateProcedure
	for _, rec := range records {
		if !consolidationSourceActive(rec.Lifecycle) || rec.RunID == "" {
			continue
		}
		if outcome.Kind == model.MemoryKindFailure {
			mode = ConsolidateAntiPattern
			if rec.Kind != model.MemoryKindFailure || metaString(rec.ExtMeta, "error_signature") != metaString(outcome.ExtMeta, "error_signature") ||
				metaString(rec.ExtMeta, "retry_condition") != metaString(outcome.ExtMeta, "retry_condition") {
				continue
			}
			left, leftErr := stableJSONDigest(rec.ExtMeta["environment"])
			right, rightErr := stableJSONDigest(outcome.ExtMeta["environment"])
			if leftErr != nil || rightErr != nil || left != right {
				continue
			}
		} else if rec.Kind != model.MemoryKindEpisodic || metaString(rec.ExtMeta, "outcome_status") != "success" {
			continue
		}
		sourceIDs = append(sourceIDs, rec.ID)
		if len(sourceIDs) == maxConsolidationSources {
			break
		}
	}
	if len(uniqueSortedNonEmpty(sourceIDs)) < minConsolidationSources {
		return nil, nil
	}
	result, err := s.ProposeConsolidation(ctx, principal, ConsolidationRequest{
		ProjectID: outcome.ProjectID, Mode: mode, Subject: subject,
		Scope: model.ScopeTask, ScopeID: outcome.ScopeID, SourceMemoryIDs: sourceIDs,
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// ProposeConsolidation derives a bounded, low-authority candidate from
// canonical source records. It never promotes, tombstones, or rewrites source
// truth. A policy/operator review remains mandatory for durable promotion.
func (s *MemoryService) ProposeConsolidation(ctx context.Context, principal authz.Principal, req ConsolidationRequest) (ConsolidationResult, error) {
	if err := ctx.Err(); err != nil {
		return ConsolidationResult{}, err
	}
	if s == nil || s.store == nil {
		return ConsolidationResult{}, fmt.Errorf("%w: memory store is unavailable", model.ErrUnavailable)
	}
	req.ProjectID = strings.TrimSpace(req.ProjectID)
	req.ScopeID = strings.TrimSpace(req.ScopeID)
	req.Subject = strings.TrimSpace(req.Subject)
	if req.ProjectID == "" || req.ScopeID == "" || !req.Mode.valid() {
		return ConsolidationResult{}, fmt.Errorf("%w: project, valid mode, scope, and scope_id are required", model.ErrInvalid)
	}
	if req.Scope != model.ScopeProject && req.Scope != model.ScopeTask && req.Scope != model.ScopeAgent && req.Scope != model.ScopeOperatorPrivate {
		return ConsolidationResult{}, fmt.Errorf("%w: unsupported consolidation scope %q", model.ErrInvalid, req.Scope)
	}
	if _, err := model.NewMemoryScope(string(req.Scope), req.ScopeID); err != nil {
		return ConsolidationResult{}, err
	}
	if req.Scope == model.ScopeProject && req.ScopeID != req.ProjectID {
		return ConsolidationResult{}, authz.ErrUnauthorized
	}
	if (req.Scope == model.ScopeAgent || req.Scope == model.ScopeOperatorPrivate) && req.ScopeID != principal.ID && !principalHasAuthority(principal, authz.AuthorityPolicyAdmin) {
		return ConsolidationResult{}, authz.ErrUnauthorized
	}
	if err := s.authorizer.Authorize(ctx, principal, authz.ActionMemoryRemember, req.ScopeID, model.MemoryCandidate); err != nil {
		return ConsolidationResult{}, err
	}
	if req.Scope == model.ScopeTask {
		if err := s.authorizeTaskScope(ctx, principal, authz.ActionMemoryRemember, req.ScopeID); err != nil {
			return ConsolidationResult{}, err
		}
	}

	sourceIDs := uniqueSortedNonEmpty(req.SourceMemoryIDs)
	if len(sourceIDs) < minConsolidationSources || len(sourceIDs) > maxConsolidationSources {
		return ConsolidationResult{}, fmt.Errorf("%w: consolidation requires %d-%d distinct source records", model.ErrInvalid, minConsolidationSources, maxConsolidationSources)
	}
	sources := make([]model.MemoryRecordV2, 0, len(sourceIDs))
	for _, sourceID := range sourceIDs {
		rec, err := s.store.GetMemoryV2(ctx, req.ProjectID, sourceID)
		if err != nil {
			return ConsolidationResult{}, fmt.Errorf("load consolidation source %s: %w", sourceID, err)
		}
		if rec.Scope != string(req.Scope) || rec.ScopeID != req.ScopeID {
			return ConsolidationResult{}, authz.ErrUnauthorized
		}
		if rec.ACLScope != "" && rec.ACLScope != principal.ID && !principalHasAuthority(principal, authz.AuthorityPolicyAdmin) {
			return ConsolidationResult{}, authz.ErrUnauthorized
		}
		if !consolidationSourceActive(rec.Lifecycle) {
			return ConsolidationResult{}, fmt.Errorf("%w: source %s has inactive lifecycle %s", model.ErrConflict, rec.ID, rec.Lifecycle)
		}
		sources = append(sources, rec)
	}

	candidate, signature, err := buildConsolidationCandidate(req, principal.ID, sources, sourceIDs)
	if err != nil {
		return ConsolidationResult{}, err
	}
	if len(candidate.EvidenceIDs) > maxConsolidationEvidence {
		return ConsolidationResult{}, fmt.Errorf("%w: consolidation evidence exceeds %d IDs", model.ErrInvalid, maxConsolidationEvidence)
	}
	for _, evidenceID := range candidate.EvidenceIDs {
		if len(evidenceID) > 256 {
			return ConsolidationResult{}, fmt.Errorf("%w: consolidation evidence ID exceeds 256 bytes", model.ErrInvalid)
		}
	}
	if err := s.linkPriorConsolidationProposals(ctx, principal, &candidate, req.Mode, signature, sourceIDs); err != nil {
		return ConsolidationResult{}, err
	}
	if err := security.NewFirewall(security.FirewallConfig{}).ScanRecord(ctx, candidate); err != nil {
		return ConsolidationResult{}, err
	}

	if err := s.store.WriteMemoryV2(ctx, candidate); err != nil {
		// The stable ID makes retries and concurrent identical requests
		// idempotent without a process-global consolidation lock.
		existing, getErr := s.store.GetMemoryV2(ctx, req.ProjectID, candidate.ID)
		if getErr == nil && sameConsolidationIdentity(existing, candidate) {
			return ConsolidationResult{Candidate: existing, Existing: true}, nil
		}
		return ConsolidationResult{}, err
	}
	if err := s.IndexRecord(ctx, candidate); err != nil {
		return ConsolidationResult{}, fmt.Errorf("index consolidation candidate: %w", err)
	}
	return ConsolidationResult{Candidate: candidate}, nil
}

func (m ConsolidationMode) valid() bool {
	return m == ConsolidateProcedure || m == ConsolidateAntiPattern || m == ConsolidateVerifiedFact
}

func consolidationSourceActive(lifecycle model.MemoryLifecycle) bool {
	switch lifecycle {
	case model.MemoryCandidate, model.MemoryVerified, model.MemoryDurable:
		return true
	default:
		return false
	}
}

func buildConsolidationCandidate(req ConsolidationRequest, actorID string, sources []model.MemoryRecordV2, sourceIDs []string) (model.MemoryRecordV2, string, error) {
	evidenceIDs := make([]string, 0)
	for _, rec := range sources {
		evidenceIDs = append(evidenceIDs, rec.EvidenceIDs...)
	}
	evidenceIDs = uniqueSortedNonEmpty(evidenceIDs)
	aclScope, err := commonACL(sources)
	if err != nil {
		return model.MemoryRecordV2{}, "", err
	}

	var (
		kind      model.MemoryKind
		title     string
		body      string
		signature string
		meta      map[string]any
		conflicts []string
	)
	switch req.Mode {
	case ConsolidateProcedure:
		kind, title, body, signature, meta, err = buildProcedureProposal(req.Subject, sources)
	case ConsolidateAntiPattern:
		kind, title, body, signature, meta, err = buildAntiPatternProposal(sources)
	case ConsolidateVerifiedFact:
		kind, title, body, signature, meta, conflicts, err = buildVerifiedFactProposal(req.Subject, sources)
	}
	if err != nil {
		return model.MemoryRecordV2{}, "", err
	}

	now := time.Now().UTC()
	stableID := consolidationID(req.ProjectID, req.Mode, req.Scope, req.ScopeID, signature, sourceIDs)
	meta["consolidation_mode"] = string(req.Mode)
	meta["consolidation_signature"] = signature
	meta["source_memory_ids"] = append([]string(nil), sourceIDs...)
	meta["source_count"] = len(sourceIDs)
	meta["requires_governance_review"] = true
	rec := model.MemoryRecordV2{
		ID: stableID, ProjectID: req.ProjectID, Kind: kind, Lifecycle: model.MemoryCandidate,
		Confidence: model.ConfidenceInferred, Authority: model.AuthorityAgent,
		Title: title, Body: body, Scope: string(req.Scope), ScopeID: req.ScopeID, ACLScope: aclScope,
		Source:      model.MemorySource{Kind: "consolidation_candidate", Reference: signature, AgentID: actorID},
		EvidenceIDs: evidenceIDs, ConflictIDs: conflicts,
		ObservedAt: now, IngestedAt: now, ValidFrom: now, CreatedAt: now, UpdatedAt: now,
		ExtMeta: meta,
	}
	if len(conflicts) > 0 {
		rec.Lifecycle = model.MemoryConflicted
	}
	applyCommonRepositoryProvenance(&rec, sources)
	return rec, signature, nil
}

func buildProcedureProposal(subject string, sources []model.MemoryRecordV2) (model.MemoryKind, string, string, string, map[string]any, error) {
	if subject == "" || len(subject) > 256 {
		return "", "", "", "", nil, fmt.Errorf("%w: bounded procedure subject is required", model.ErrInvalid)
	}
	runIDs := make(map[string]struct{})
	var tests, files [][]string
	for _, rec := range sources {
		if rec.Kind != model.MemoryKindEpisodic || metaString(rec.ExtMeta, "outcome_status") != "success" || rec.RunID == "" {
			return "", "", "", "", nil, fmt.Errorf("%w: procedure sources must be distinct successful episodes", model.ErrInvalid)
		}
		runIDs[rec.RunID] = struct{}{}
		tests = append(tests, metaStringSlice(rec.ExtMeta, "tests_run"))
		files = append(files, metaStringSlice(rec.ExtMeta, "files_changed"))
	}
	if len(runIDs) < minConsolidationSources {
		return "", "", "", "", nil, fmt.Errorf("%w: repeated copies of one run are not independent evidence", model.ErrInvalid)
	}
	commonTests := boundedConsolidationStrings(intersectStrings(tests), 16, 512)
	commonFiles := boundedConsolidationStrings(intersectStrings(files), 16, 512)
	body := fmt.Sprintf("Repeated successful outcomes for %s were observed in %d independent runs. Common verified tests: %s. Common changed files: %s. This is a candidate procedure and not an instruction or approval.",
		subject, len(runIDs), displayStrings(commonTests), displayStrings(commonFiles))
	body = truncateMemoryField(body, 16<<10)
	signature := normalizedSignature(subject)
	return model.MemoryKindProcedural, "Procedure candidate: " + subject, body, signature, map[string]any{
		"successful_run_count": len(runIDs), "common_tests": commonTests, "common_files": commonFiles,
	}, nil
}

func buildAntiPatternProposal(sources []model.MemoryRecordV2) (model.MemoryKind, string, string, string, map[string]any, error) {
	runIDs := make(map[string]struct{})
	var errorSignature, retryCondition, environmentDigest string
	var environment any
	for _, rec := range sources {
		if rec.Kind != model.MemoryKindFailure || metaString(rec.ExtMeta, "outcome_status") == "success" || rec.RunID == "" {
			return "", "", "", "", nil, fmt.Errorf("%w: anti-pattern sources must be distinct failed episodes", model.ErrInvalid)
		}
		runIDs[rec.RunID] = struct{}{}
		recSignature := metaString(rec.ExtMeta, "error_signature")
		recRetry := metaString(rec.ExtMeta, "retry_condition")
		recEnvironment := rec.ExtMeta["environment"]
		recEnvironmentDigest, err := stableJSONDigest(recEnvironment)
		if err != nil || recSignature == "" || recRetry == "" || emptyEnvironment(recEnvironment) {
			return "", "", "", "", nil, fmt.Errorf("%w: failure sources require error signature, retry condition, and environment", model.ErrInvalid)
		}
		if errorSignature == "" {
			errorSignature, retryCondition, environmentDigest, environment = recSignature, recRetry, recEnvironmentDigest, recEnvironment
			continue
		}
		if recSignature != errorSignature || recRetry != retryCondition || recEnvironmentDigest != environmentDigest {
			return "", "", "", "", nil, fmt.Errorf("%w: failure lessons cannot widen across signatures, retry conditions, or environments", model.ErrConflict)
		}
	}
	if len(runIDs) < minConsolidationSources {
		return "", "", "", "", nil, fmt.Errorf("%w: repeated copies of one failed run are not independent evidence", model.ErrInvalid)
	}
	signature := normalizedSignature(errorSignature + ":" + environmentDigest)
	body := fmt.Sprintf("The failure signature %s repeated in %d independent runs in one bounded environment. Retry condition: %s. This environment-scoped candidate must not blacklist the approach elsewhere.", errorSignature, len(runIDs), retryCondition)
	return model.MemoryKindFailure, "Anti-repeat candidate: " + errorSignature, body, signature, map[string]any{
		"failed_run_count": len(runIDs), "error_signature": errorSignature, "retry_condition": retryCondition,
		"environment": environment, "environment_digest": environmentDigest, "environment_scoped": true,
	}, nil
}

func buildVerifiedFactProposal(subject string, sources []model.MemoryRecordV2) (model.MemoryKind, string, string, string, map[string]any, []string, error) {
	first := sources[0]
	if first.Kind != model.MemoryKindFinding && first.Kind != model.MemoryKindSemantic && first.Kind != model.MemoryKindDecision {
		return "", "", "", "", nil, nil, fmt.Errorf("%w: unsupported verified fact kind %s", model.ErrInvalid, first.Kind)
	}
	if subject == "" {
		subject = strings.TrimSpace(first.Title)
	}
	if subject == "" || len(subject) > 256 {
		return "", "", "", "", nil, nil, fmt.Errorf("%w: bounded fact subject is required", model.ErrInvalid)
	}
	normalizedBody := normalizedFact(first.Body)
	normalizedTitle := normalizedFact(first.Title)
	conflicts := make([]string, 0)
	for _, rec := range sources {
		if rec.Kind != first.Kind || rec.Confidence != model.ConfidenceVerified {
			return "", "", "", "", nil, nil, fmt.Errorf("%w: fact sources must have the same kind and verified confidence", model.ErrInvalid)
		}
		if normalizedFact(rec.Body) != normalizedBody || normalizedFact(rec.Title) != normalizedTitle {
			conflicts = append(conflicts, rec.ID)
		}
	}
	signature := normalizedSignature(subject)
	if len(conflicts) > 0 {
		allSourceIDs := make([]string, 0, len(sources))
		for _, rec := range sources {
			allSourceIDs = append(allSourceIDs, rec.ID)
		}
		return first.Kind, "Conflicting fact candidate: " + subject,
			"Verified source records disagree for this subject. Governance must reconcile the linked canonical records; no source was overwritten.",
			signature, map[string]any{"verified_source_count": len(sources), "conflict_requires_reconciliation": true}, uniqueSortedNonEmpty(allSourceIDs), nil
	}
	return first.Kind, "Consolidated fact candidate: " + subject,
		fmt.Sprintf("%s (Observed consistently in %d verified source records; promotion still requires governance.)", truncateMemoryField(strings.TrimSpace(first.Body), 16<<10), len(sources)),
		signature, map[string]any{"verified_source_count": len(sources)}, nil, nil
}

func (s *MemoryService) linkPriorConsolidationProposals(ctx context.Context, principal authz.Principal, candidate *model.MemoryRecordV2, mode ConsolidationMode, signature string, sourceIDs []string) error {
	records, err := s.store.ListMemoryV2(ctx, store.MemoryQueryFilter{
		ProjectID: candidate.ProjectID, Scope: model.MemoryScopeKind(candidate.Scope), ScopeID: candidate.ScopeID, ActorID: principal.ID,
		Limit: maxConsolidationSources * 4,
	})
	if err != nil {
		return err
	}
	current := stringSet(sourceIDs)
	for _, prior := range records {
		if prior.ID == candidate.ID || prior.Source.Kind != "consolidation_candidate" ||
			metaString(prior.ExtMeta, "consolidation_mode") != string(mode) ||
			metaString(prior.ExtMeta, "consolidation_signature") != signature ||
			!consolidationSourceActive(prior.Lifecycle) {
			continue
		}
		priorSources := stringSet(metaStringSlice(prior.ExtMeta, "source_memory_ids"))
		if strictSuperset(current, priorSources) {
			candidate.SupersedesID = append(candidate.SupersedesID, prior.ID)
		} else {
			candidate.Lifecycle = model.MemoryConflicted
			candidate.ConflictIDs = append(candidate.ConflictIDs, prior.ID)
		}
	}
	candidate.SupersedesID = uniqueSortedNonEmpty(candidate.SupersedesID)
	candidate.ConflictIDs = uniqueSortedNonEmpty(candidate.ConflictIDs)
	return nil
}

func sameConsolidationIdentity(a, b model.MemoryRecordV2) bool {
	return a.ID == b.ID && a.ProjectID == b.ProjectID && a.Scope == b.Scope && a.ScopeID == b.ScopeID &&
		a.ACLScope == b.ACLScope && a.Kind == b.Kind && a.Title == b.Title && a.Body == b.Body &&
		a.Authority == model.AuthorityAgent && a.Source.Kind == "consolidation_candidate" && consolidationSourceActive(a.Lifecycle) &&
		strings.Join(uniqueSortedNonEmpty(a.EvidenceIDs), "\x00") == strings.Join(uniqueSortedNonEmpty(b.EvidenceIDs), "\x00") &&
		strings.Join(metaStringSlice(a.ExtMeta, "source_memory_ids"), "\x00") == strings.Join(metaStringSlice(b.ExtMeta, "source_memory_ids"), "\x00") &&
		metaString(a.ExtMeta, "consolidation_mode") == metaString(b.ExtMeta, "consolidation_mode") &&
		metaString(a.ExtMeta, "consolidation_signature") == metaString(b.ExtMeta, "consolidation_signature")
}

func consolidationID(projectID string, mode ConsolidationMode, scope model.MemoryScopeKind, scopeID, signature string, sourceIDs []string) string {
	h := sha256.New()
	for _, value := range append([]string{projectID, string(mode), string(scope), scopeID, signature}, sourceIDs...) {
		_, _ = h.Write([]byte(value))
		_, _ = h.Write([]byte{0})
	}
	return "MEM-CONS-" + hex.EncodeToString(h.Sum(nil))[:24]
}

func commonACL(sources []model.MemoryRecordV2) (string, error) {
	acl := sources[0].ACLScope
	for _, rec := range sources[1:] {
		if rec.ACLScope != acl {
			return "", fmt.Errorf("%w: consolidation cannot widen mixed ACL scopes", authz.ErrUnauthorized)
		}
	}
	return acl, nil
}

func applyCommonRepositoryProvenance(candidate *model.MemoryRecordV2, sources []model.MemoryRecordV2) {
	candidate.HeadCommit = sources[0].HeadCommit
	candidate.BranchName = sources[0].BranchName
	candidate.WorktreeID = sources[0].WorktreeID
	for _, rec := range sources[1:] {
		if rec.HeadCommit != candidate.HeadCommit {
			candidate.HeadCommit = ""
			candidate.ExtMeta["mixed_repository_heads"] = true
		}
		if rec.BranchName != candidate.BranchName {
			candidate.BranchName = ""
			candidate.ExtMeta["mixed_repository_branches"] = true
		}
		if rec.WorktreeID != candidate.WorktreeID {
			candidate.WorktreeID = ""
			candidate.ExtMeta["mixed_worktrees"] = true
		}
	}
}

func metaString(meta map[string]any, key string) string {
	value, _ := meta[key].(string)
	return strings.TrimSpace(value)
}

func metaStringSlice(meta map[string]any, key string) []string {
	switch values := meta[key].(type) {
	case []string:
		return uniqueSortedNonEmpty(values)
	case []any:
		result := make([]string, 0, len(values))
		for _, value := range values {
			if text, ok := value.(string); ok {
				result = append(result, text)
			}
		}
		return uniqueSortedNonEmpty(result)
	default:
		return nil
	}
}

func uniqueSortedNonEmpty(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func intersectStrings(groups [][]string) []string {
	if len(groups) == 0 {
		return nil
	}
	counts := make(map[string]int)
	for _, value := range uniqueSortedNonEmpty(groups[0]) {
		counts[value] = 1
	}
	for groupIndex, group := range groups[1:] {
		seen := stringSet(group)
		for value, count := range counts {
			if count == groupIndex+1 {
				if _, ok := seen[value]; ok {
					counts[value]++
				}
			}
		}
	}
	result := make([]string, 0)
	for value, count := range counts {
		if count == len(groups) {
			result = append(result, value)
		}
	}
	sort.Strings(result)
	if len(result) > 32 {
		result = result[:32]
	}
	return result
}

func displayStrings(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ", ")
}

func normalizedSignature(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), " "))
}

func normalizedFact(value string) string {
	return normalizedSignature(value)
}

func stableJSONDigest(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	if len(encoded) > 8<<10 {
		return "", fmt.Errorf("%w: environment metadata exceeds 8192 bytes", model.ErrInvalid)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func boundedConsolidationStrings(values []string, maxItems, maxBytes int) []string {
	if len(values) > maxItems {
		values = values[:maxItems]
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, truncateMemoryField(value, maxBytes))
	}
	return result
}

func emptyEnvironment(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case map[string]string:
		return len(typed) == 0
	case map[string]any:
		return len(typed) == 0
	default:
		return false
	}
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func strictSuperset(candidate, prior map[string]struct{}) bool {
	if len(candidate) <= len(prior) {
		return false
	}
	for value := range prior {
		if _, ok := candidate[value]; !ok {
			return false
		}
	}
	return true
}
