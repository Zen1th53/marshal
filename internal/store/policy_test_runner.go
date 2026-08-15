package store

import (
	"context"
	"fmt"
	"sort"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/policy"
	"github.com/Zen1th53/marshal/internal/policytest"
)

// RunPolicyTest executes a validated suite against its exact bound policy
// snapshot. Case outcomes are returned as an ephemeral CI result; durable
// security history is the bounded A05 case event stream, while the immutable
// A02 run projection remains unchanged.
func (s *Store) RunPolicyTest(ctx context.Context, request policytest.RunRequest) (policytest.RunResult, error) {
	if request.Authorizer == nil || request.Evaluator == nil {
		return policytest.RunResult{}, policy.ErrAuthorizationUnavailable
	}
	if err := request.TestFileDigest.Validate(); err != nil {
		return policytest.RunResult{}, err
	}
	suite, err := policytest.NewSuite(request.Suite)
	if err != nil {
		return policytest.RunResult{}, err
	}
	run, err := s.GetPolicyTestRun(ctx, request.RunID)
	if err != nil {
		return policytest.RunResult{}, err
	}
	if run.TestFileDigest != request.TestFileDigest || len(run.Cases) == 0 || len(run.Cases) != len(suite.Cases) {
		return policytest.RunResult{}, policytest.ErrCaseInvalid
	}
	caseIDs := make(map[policytest.CaseID]struct{}, len(run.Cases))
	for _, stored := range run.Cases {
		caseIDs[stored.ID] = struct{}{}
	}
	for _, testCase := range suite.Cases {
		if testCase.Given.Policy.ID != run.PolicyID || testCase.Given.Binding != run.Binding {
			return policytest.RunResult{}, policytest.ErrCaseInvalid
		}
		if _, ok := caseIDs[testCase.ID]; !ok {
			return policytest.RunResult{}, policytest.ErrCaseInvalid
		}
		delete(caseIDs, testCase.ID)
	}
	if len(caseIDs) != 0 {
		return policytest.RunResult{}, policytest.ErrCaseInvalid
	}
	if err := ctx.Err(); err != nil {
		return policytest.RunResult{}, err
	}
	if run.State == policytest.StateLoaded {
		run, err = s.authorizedRunTransition(ctx, request, run, policytest.StateValidated)
		if err != nil {
			return policytest.RunResult{}, err
		}
	}
	if run.State == policytest.StateValidated {
		run, err = s.authorizedRunTransition(ctx, request, run, policytest.StateExecuted)
		if err != nil {
			return policytest.RunResult{}, err
		}
	}
	if run.State != policytest.StateExecuted {
		return policytest.RunResult{}, policytest.ErrIllegalTransition
	}
	result, err := policytest.RunSuite(ctx, suite, request.Evaluator)
	if err != nil {
		return policytest.RunResult{}, err
	}
	facts := make([]policytest.EventFact, 0, len(result.Cases))
	for _, caseResult := range result.Cases {
		if caseResult.Result.Status != policytest.StatusPass && caseResult.Result.Status != policytest.StatusFail && caseResult.Result.Status != policytest.StatusError {
			continue
		}
		fact := policytest.EventFact{
			Type:           policytest.EventCaseFailed,
			RunID:          run.ID,
			CaseID:         caseResult.ID,
			PolicyID:       run.PolicyID,
			PolicyVersion:  run.Binding.Version,
			PolicyDigest:   run.Binding.Digest,
			Generation:     run.Binding.Generation,
			TestFileDigest: run.TestFileDigest,
			PreviousState:  policytest.StateExecuted,
			TargetState:    policytest.StateExecuted,
			Action:         policytest.ActionTransition,
			SubjectID:      request.SubjectID,
			SessionID:      request.SessionID,
			TaskID:         request.TaskID,
			ChangeID:       request.ChangeID,
			Result:         "failed",
			ReasonCode:     caseResult.Result.Reason,
		}
		if caseResult.Result.Status == policytest.StatusPass {
			fact.Type = policytest.EventCasePassed
			fact.Result = "passed"
			fact.ReasonCode = policy.CodeAuthorizationAllowed
		}
		facts = append(facts, fact)
	}
	target := policytest.StateFailed
	if result.Status == policytest.StatusPass {
		target = policytest.StatePassed
	}
	if _, err := s.authorizedRunTransitionWithCaseEvents(ctx, request, run, target, facts); err != nil {
		return policytest.RunResult{}, err
	}
	return result, nil
}

func (s *Store) authorizedRunTransitionWithCaseEvents(ctx context.Context, base policytest.RunRequest, run policytest.TestRun, target policytest.RunState, facts []policytest.EventFact) (policytest.TestRun, error) {
	authRequest := policytest.AuthorizationRequest{
		SubjectID: base.SubjectID, SessionID: base.SessionID, TaskID: base.TaskID, ChangeID: base.ChangeID,
		RunID: run.ID, PolicyID: run.PolicyID, Binding: run.Binding, TestFileDigest: run.TestFileDigest,
		ExpectedState: run.State, TargetState: target, Action: policytest.ActionTransition,
	}
	if err := authRequest.Validate(); err != nil {
		return policytest.TestRun{}, err
	}
	if base.Authorizer == nil {
		return policytest.TestRun{}, policy.ErrAuthorizationUnavailable
	}
	decision, err := base.Authorizer.AuthorizePolicyTestRun(ctx, authRequest)
	if err != nil {
		return policytest.TestRun{}, policy.ErrAuthorizationUnavailable
	}
	if err := decision.ValidateFor(authRequest); err != nil {
		return policytest.TestRun{}, err
	}
	fact, emit := policytest.EventFact{}, false
	if candidate, ok := policytest.EventForTransition(authRequest); ok {
		fact, emit = candidate, true
	}
	sort.Slice(facts, func(i, j int) bool { return facts[i].CaseID < facts[j].CaseID })
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return policytest.TestRun{}, fmt.Errorf("%w: policy test transition unavailable", model.ErrUnavailable)
	}
	defer tx.Rollback()
	var lifecycle *policytest.EventFact
	if emit {
		lifecycle = &fact
	}
	if err := s.transitionPolicyTestRunStateTx(ctx, tx, authRequest.RunID, authRequest.ExpectedState, authRequest.TargetState, &authRequest, lifecycle); err != nil {
		return policytest.TestRun{}, err
	}
	for _, caseFact := range facts {
		if err := s.appendPolicyTestEvent(ctx, tx, caseFact); err != nil {
			return policytest.TestRun{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return policytest.TestRun{}, fmt.Errorf("%w: policy test transition unavailable", model.ErrUnavailable)
	}
	return s.GetPolicyTestRun(ctx, authRequest.RunID)
}

func (s *Store) authorizedRunTransition(ctx context.Context, base policytest.RunRequest, run policytest.TestRun, target policytest.RunState) (policytest.TestRun, error) {
	authRequest := policytest.AuthorizationRequest{
		SubjectID: base.SubjectID, SessionID: base.SessionID, TaskID: base.TaskID, ChangeID: base.ChangeID,
		RunID: run.ID, PolicyID: run.PolicyID, Binding: run.Binding, TestFileDigest: run.TestFileDigest,
		ExpectedState: run.State, TargetState: target, Action: policytest.ActionTransition,
	}
	return s.TransitionPolicyTestRunStateAuthorized(ctx, authRequest, base.Authorizer)
}
