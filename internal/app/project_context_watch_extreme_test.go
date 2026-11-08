//go:build !governed

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/capability"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/store"
)

// Real canonical Goal/plan/materialized task and separate proposal/Decision
// acceptance. Provider waits below are mocks, never a native authority proof.
func requiredWatchFixture(t *testing.T, beta bool) (*Runtime, projectcontext.Receipt, store.ProjectContextAdmission, model.MemoryRecordV2) {
	t.Helper()
	r, b, rec, state := requiredContextFixture(t)
	if beta {
		accepted, err := r.store.ReadAcceptedProjectContract(t.Context(), b.Project)
		if err != nil {
			t.Fatal(err)
		}
		other := rec
		other.ID = "MEM-private-beta"
		other.Body = "Private beta policy"
		other.ACLScope = "owner"
		other.ContentDigest = other.CanonicalDigest()
		if err = r.store.WriteMemoryV2(t.Context(), other); err != nil {
			t.Fatal(err)
		}
		next := accepted.Contract
		next.Required = append(append([]projectcontext.MemoryRef{}, next.Required...), projectcontext.MemoryRef{ID: other.ID, ProjectID: other.ProjectID, Scope: other.Scope, ScopeID: other.ScopeID, Revision: other.Revision, ContentDigest: other.ContentDigest, StateFingerprint: projectcontext.MemoryFingerprint(other), Class: projectcontext.Policy, Applies: projectcontext.Applicability{TaskIDs: []string{"beta"}}, Reason: "Reviewed beta-only policy", AcceptanceOccurrence: "owner-beta"})
		current := acceptWatchContract(t, r, next, "initial-beta")
		digest, _ := current.CanonicalDigest()
		b.Contract = projectcontext.ContractRef{ID: current.ID, Revision: current.Revision, Digest: digest}
	}
	goal, err := r.store.GetActiveGoalContract(t.Context(), b.Session)
	if err != nil {
		t.Fatal(err)
	}
	request := planCreateRequest()
	request.SessionID = b.Session
	request.ProjectID = projectid.ID(r.ProjectIdentity())
	if _, err = r.Plans().Create(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	p, err := r.Plans().Approve(t.Context(), request.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	handoff, err := plan.PrepareHandoff(p, goal, p.ProjectID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Execution().materializeCanonicalTasks(t.Context(), &handoff, goal.Risk, b.Session); err != nil {
		t.Fatal(err)
	}
	b.Principal = "AGENT-watch-codex"
	b.Provider = "codex"
	b.Model = "selected-model"
	b.TaskID = p.Tasks[0].ID
	b.CanonicalTaskID = handoff.CanonicalTaskIDs[b.TaskID]
	b.PlanID = p.ID
	b.PlanVersion = p.Version
	b.ApprovalScopeDigest = p.ApprovalScopeDigest
	b.Attempt = 1
	if err = r.store.RegisterAgent(t.Context(), model.Agent{ID: b.Principal, ProjectID: r.ProjectID(), DisplayName: "watch worker", Role: model.RoleDeveloper, ModelProvider: b.Provider, ModelName: "configured-model", Status: model.AgentRegistered, Revision: 1}); err != nil {
		t.Fatal(err)
	}
	if err = r.store.PutRoleBinding(t.Context(), authz.RoleBinding{ID: "ROLE-watch", PrincipalID: b.Principal, Role: "developer", ScopeID: b.CanonicalTaskID, BoundBy: "owner", BoundAt: time.Now().UTC().Add(-time.Hour), PolicyDigest: "sha256:" + strings.Repeat("a", 64)}); err != nil {
		t.Fatal(err)
	}
	accepted, err := r.store.ReadAcceptedProjectContract(t.Context(), b.Project)
	if err != nil {
		t.Fatal(err)
	}
	selection, err := requiredProjectPlanSelection(p, b, accepted.Contract)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(t.Context(), requiredProjectWorkspaceKey{}, requiredProjectWorkspaceProof{Runtime: r, Read: func(context.Context, projectcontext.Binding) (requiredProjectWorkspaceState, error) {
		return state, nil
	}})
	prepared, err := r.prepareRequiredProjectContext(ctx, authz.Principal{ID: b.Principal}, b, selection, "full admitted provider request\n", 16384)
	if err != nil {
		t.Fatal(err)
	}
	a := prepared.Admission
	a.ID = "CTX-watch"
	a.Now = time.Now().UTC()
	observed, err := r.store.ReadProjectContextAuthority(t.Context(), store.ProjectContextAuthorityRequest{Binding: a.Binding, Purpose: store.ProjectContextAuthorityWorker}, a.Now)
	if err != nil {
		t.Fatal(err)
	}
	a.ExpectedAuthority = &observed
	receipt, err := r.store.CommitProjectContextAdmission(t.Context(), a)
	if err != nil {
		t.Fatal(err)
	}
	a.Binding = receipt.Binding
	return r, receipt, a, rec
}

func acceptWatchContract(t *testing.T, r *Runtime, next projectcontext.Contract, suffix string) projectcontext.Contract {
	t.Helper()
	previous, err := r.store.ReadAcceptedProjectContract(t.Context(), next.Project)
	if err != nil {
		t.Fatal(err)
	}
	next.Revision = previous.Version + 1
	next.SupersedesDigest = previous.Digest
	next.RevisionReason = "Reviewed " + suffix
	audience := store.ProjectContextAudience{Actor: "owner", ScopeIDs: []string{r.ProjectID()}}
	_, inventory, err := r.store.ProjectContractInventory(t.Context(), next.Project, audience)
	if err != nil {
		t.Fatal(err)
	}
	next.ReviewedInventoryDigest = inventory
	source, _, err := r.store.AdmitMCPProjectContract(t.Context(), store.MCPProposal{ID: "mcp:watch-contract-" + suffix, Launch: "watch-launch", Owner: "owner", Provider: "codex", Submission: suffix}, store.ProjectContractSubmission{Action: "project-contract", ExpectedVersion: previous.Version, Contract: next})
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := next.CanonicalDigest()
	binding, _ := json.Marshal(store.ProjectContractApprovalBinding{ProposalDigest: source.Digest, ContractDigest: digest, InventoryDigest: inventory, ExpectedVersion: previous.Version})
	req := permission.Request{Kind: "project-contract", Object: next.ID, Scope: r.ProjectID(), Who: "Marshal", Reason: "Review exact contract", ProposalID: source.ID, ApprovalBinding: string(binding), SourceLaunch: source.Launch, SourceOccurrence: "mcp:watch-decision-" + suffix}
	raw, _ := json.Marshal(map[string]any{"action": "decide", "id": "QUESTION-" + suffix, "allow": true, "reason": "Reviewed exact original contract"})
	if _, _, err = r.store.AdmitMCPProposal(t.Context(), r.ProjectID(), store.MCPProposal{ID: req.SourceOccurrence, Launch: source.Launch, Owner: "owner", Provider: "codex", Submission: "decision-" + suffix, Payload: string(raw)}); err != nil {
		t.Fatal(err)
	}
	request, _ := json.Marshal(req)
	if err = r.store.AcceptMCPDecision(t.Context(), r.ProjectID(), req.SourceOccurrence, string(request), req.Key()); err != nil {
		t.Fatal(err)
	}
	if err = r.store.SetMCPProposalState(t.Context(), r.ProjectID(), req.SourceOccurrence, "applying", "protected test effect"); err != nil {
		t.Fatal(err)
	}
	current, err := r.store.CommitMCPProjectContractDecision(t.Context(), req, true, audience, time.Now().UTC(), model.Event{ID: "watch-accepted-" + suffix, ProjectID: r.ProjectID(), Type: "PERMISSION_DECIDED", Timestamp: time.Now().UTC(), Data: map[string]any{"source_occurrence": req.SourceOccurrence}})
	if err != nil {
		t.Fatal(err)
	}
	return current
}

func TestRequiredContextWatchRevocationCancelsOnlyOwnedMockTurnAndQuarantines(t *testing.T) {
	for _, mutation := range []string{"role", "acl", "goal", "architecture", "unclassified", "store-unavailable", "parent-cancel", "capability"} {
		t.Run(mutation, func(t *testing.T) {
			r, receipt, a, record := requiredWatchFixture(t, false)
			if mutation == "capability" {
				if err := r.store.PutCapabilityGrant(t.Context(), capability.Grant{ID: "CAP-watch", Subject: capability.SubjectID(a.Binding.Principal), TaskID: capability.TaskID(a.Binding.CanonicalTaskID), Kind: capability.KindFilesystemRead, Scope: capability.Scope{Resource: "README.md", Actions: []string{"read"}}, Issuer: "owner", IssuedAt: time.Now().UTC().Add(-time.Hour), ExpiresAt: time.Now().UTC().Add(time.Hour)}); err != nil {
					t.Fatal(err)
				}
				observed, err := r.store.ReadProjectContextAuthority(t.Context(), a.ExpectedAuthority.Request, time.Now().UTC())
				if err != nil {
					t.Fatal(err)
				}
				a.ExpectedAuthority = &observed
				a.ID = "CTX-watch-with-cap"
				a.Now = time.Now().UTC()
				receipt, err = r.store.CommitProjectContextAdmission(t.Context(), a)
				if err != nil {
					t.Fatal(err)
				}
				a.Binding = receipt.Binding
			}
			parent, cancelParent := context.WithCancel(t.Context())
			defer cancelParent()
			worker, watch, err := r.startRequiredProjectContextWatch(parent, receipt, a)
			if err != nil {
				t.Fatal(err)
			}
			defer watch.Finish()
			unrelated, stopOther := context.WithCancel(parent)
			defer stopOther()
			started := make(chan struct{})
			result := make(chan string, 1)
			go func() { close(started); <-worker.Done(); result <- "producer returned a result despite cancellation" }()
			<-started
			switch mutation {
			case "role":
				err = r.store.RevokeRoleBinding(t.Context(), "ROLE-watch", time.Now().UTC())
			case "acl":
				_, err = r.store.UpdateMemory(t.Context(), record.ProjectID, record.ID, record.Revision, func(m *model.MemoryRecordV2) error { m.ACLScope = "foreign"; return nil })
			case "goal":
				goal, e := r.store.GetActiveGoalContract(t.Context(), a.Binding.Session)
				if e != nil {
					t.Fatal(e)
				}
				goal.DesiredOutcome = "Changed current Goal"
				err = r.store.SaveGoalContract(t.Context(), goal, goal.Revision)
			case "architecture":
				accepted, e := r.store.ReadAcceptedProjectContract(t.Context(), a.Binding.Project)
				if e != nil {
					t.Fatal(e)
				}
				next := accepted.Contract
				next.Purpose = "Changed accepted project mission"
				acceptWatchContract(t, r, next, "architecture")
			case "unclassified":
				record.ID = "MEM-new-normative"
				record.ContentDigest = record.CanonicalDigest()
				err = r.store.WriteMemoryV2(t.Context(), record)
			case "store-unavailable":
				err = r.store.Close()
			case "parent-cancel":
				cancelParent()
			case "capability":
				err = r.store.RevokeCapabilityGrant(t.Context(), "CAP-watch", time.Now().UTC())
			}
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-result:
			case <-time.After(3 * time.Second):
				t.Fatal("drift did not cancel exact provider wait")
			}
			if err = watch.Finish(); err == nil {
				t.Fatal("cancel-ignoring result escaped quarantine")
			}
			if mutation != "parent-cancel" && (parent.Err() != nil || unrelated.Err() != nil) {
				t.Fatal("watcher cancelled parent or unrelated worker")
			}
			if mutation == "role" {
				events, _, _, e := r.store.ListTaskMemoryEvents(t.Context(), r.ProjectID(), a.Binding.CanonicalTaskID, 0, 10)
				if e != nil || len(events) == 0 || events[len(events)-1].EventType != "GRANT_REVOKED" {
					t.Fatal("canonical critical journal missing", events, e)
				}
			}
		})
	}
}

func TestRequiredContextWatchBetaTranslationAndAuthorizedWritesContinue(t *testing.T) {
	r, receipt, a, _ := requiredWatchFixture(t, true)
	worker, watch, err := r.startRequiredProjectContextWatch(t.Context(), receipt, a)
	if err != nil {
		t.Fatal(err)
	}
	defer watch.Finish()
	accepted, err := r.store.ReadAcceptedProjectContract(t.Context(), a.Binding.Project)
	if err != nil {
		t.Fatal(err)
	}
	beta, err := r.store.GetMemoryV2(t.Context(), r.ProjectID(), "MEM-private-beta")
	if err != nil {
		t.Fatal(err)
	}
	beta, err = r.store.UpdateMemory(t.Context(), beta.ProjectID, beta.ID, beta.Revision, func(m *model.MemoryRecordV2) error { m.Body = "Changed private beta-only policy"; return nil })
	if err != nil {
		t.Fatal(err)
	}
	next := accepted.Contract
	next.Required = append([]projectcontext.MemoryRef{}, next.Required...)
	for i := range next.Required {
		if next.Required[i].ID == beta.ID {
			next.Required[i].Revision = beta.Revision
			next.Required[i].ContentDigest = beta.ContentDigest
			next.Required[i].StateFingerprint = projectcontext.MemoryFingerprint(beta)
		}
	}
	acceptWatchContract(t, r, next, "beta-only")
	if err = os.WriteFile(filepath.Join(r.ProjectRoot(), "worker-output.txt"), []byte("authorized task output"), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-worker.Done():
		t.Fatal("beta-only accepted revision or authorized writes killed alpha")
	case <-time.After(600 * time.Millisecond):
	}
	if err = watch.Finish(); err != nil {
		t.Fatal("safe translation refused", err)
	}
	if err = watch.Finish(); err != nil {
		t.Fatal("idempotent finish failed", err)
	}
}

func TestRequiredContextWatchInitialAndFinalRefusal(t *testing.T) {
	for _, failure := range []string{"missing-authority", "forged-receipt", "selection-mismatch", "cancelled", "final-revoke", "expired"} {
		t.Run(failure, func(t *testing.T) {
			r, receipt, a, _ := requiredWatchFixture(t, false)
			ctx := t.Context()
			switch failure {
			case "missing-authority":
				a.ExpectedAuthority = nil
			case "forged-receipt":
				receipt.PacketDigest = "forged"
			case "selection-mismatch":
				a.Selection.TaskID = "beta"
			case "cancelled":
				child, cancel := context.WithCancel(ctx)
				cancel()
				ctx = child
			case "expired":
				past := time.Now().UTC().Add(-time.Second)
				receipt.ValidUntil = &past
			}
			_, watch, err := r.startRequiredProjectContextWatch(ctx, receipt, a)
			if failure == "final-revoke" {
				if err != nil {
					t.Fatal(err)
				}
				if err = r.store.RevokeRoleBinding(t.Context(), "ROLE-watch", time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
				if err = watch.Finish(); err == nil || !errors.Is(err, projectcontext.ErrChanged) {
					t.Fatal("immediate final guard missed revoke", err)
				}
			} else if err == nil {
				watch.Finish()
				t.Fatal("invalid watch began", fmt.Sprint(failure))
			}
		})
	}
}

func TestRequiredContextWatchInputIsDetachedAndReceiptExpiryStopsTurn(t *testing.T) {
	t.Run("detached input", func(t *testing.T) {
		r, receipt, a, _ := requiredWatchFixture(t, false)
		_, watch, err := r.startRequiredProjectContextWatch(t.Context(), receipt, a)
		if err != nil {
			t.Fatal(err)
		}
		if watch.admission.Prefix != "" || watch.admission.ByteCap != 0 {
			t.Fatal("watcher retained unused provider prompt")
		}
		a.Audience.ScopeIDs[0] = "forged-project"
		a.Selection.Paths[0] = "forged-path"
		a.ExpectedAuthority.Digest = "forged-digest"
		receipt.Loaded[0].StateFingerprint = "forged-state"
		if err = watch.Finish(); err != nil {
			t.Fatal("caller mutated watched baseline", err)
		}
	})
	t.Run("receipt expiry", func(t *testing.T) {
		r, _, a, _ := requiredWatchFixture(t, false)
		issued := time.Now().UTC().Add(-time.Hour)
		expires := time.Now().UTC().Add(time.Second)
		if err := r.store.PutCapabilityGrant(t.Context(), capability.Grant{ID: "CAP-short", Subject: capability.SubjectID(a.Binding.Principal), TaskID: capability.TaskID(a.Binding.CanonicalTaskID), Kind: capability.KindFilesystemRead, Scope: capability.Scope{Resource: "README.md", Actions: []string{"read"}}, Issuer: "owner", IssuedAt: issued, ExpiresAt: expires}); err != nil {
			t.Fatal(err)
		}
		observed, err := r.store.ReadProjectContextAuthority(t.Context(), a.ExpectedAuthority.Request, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		a.ExpectedAuthority = &observed
		a.ID = "CTX-expiring"
		a.Now = time.Now().UTC()
		receipt, err := r.store.CommitProjectContextAdmission(t.Context(), a)
		if err != nil {
			t.Fatal(err)
		}
		a.Binding = receipt.Binding
		worker, watch, err := r.startRequiredProjectContextWatch(t.Context(), receipt, a)
		if err != nil {
			t.Fatal(err)
		}
		defer watch.Finish()
		select {
		case <-worker.Done():
		case <-time.After(3 * time.Second):
			t.Fatal("expired receipt left turn active")
		}
		if err = watch.Finish(); !errors.Is(err, projectcontext.ErrChanged) {
			t.Fatal("expired output not quarantined", err)
		}
	})
}

// Only a read that ran out of its own deadline counts as transient; a store
// refusal or a cancelled parent never does.
func TestRequiredWatchReadTimedOutOnlyForItsOwnDeadline(t *testing.T) {
	expired, cancel := context.WithTimeout(t.Context(), time.Nanosecond)
	defer cancel()
	<-expired.Done()
	if !requiredWatchReadTimedOut(expired, context.DeadlineExceeded) {
		t.Fatal("a read past its own deadline was not transient")
	}
	live, stop := context.WithTimeout(t.Context(), time.Hour)
	defer stop()
	if requiredWatchReadTimedOut(live, errors.New("sql: database is closed")) {
		t.Fatal("a store refusal was treated as transient")
	}
	cancelled, cancelNow := context.WithCancel(t.Context())
	cancelNow()
	if requiredWatchReadTimedOut(cancelled, context.Canceled) {
		t.Fatal("a cancelled read was treated as transient")
	}
}
