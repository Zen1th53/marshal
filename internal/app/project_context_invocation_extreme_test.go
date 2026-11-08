//go:build !governed

package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/store"
)

func TestRequiredContextInvocationJoinsCanonicalAuthorityAtAdmission(t *testing.T) {
	for _, revoke := range []bool{false, true} {
		t.Run(map[bool]string{false: "current", true: "revoked-between-observation-and-commit"}[revoke], func(t *testing.T) {
			r, b, _, state := requiredContextFixture(t)
			now := time.Now().UTC()
			if err := r.store.RegisterAgent(t.Context(), model.Agent{ID: b.Principal, ProjectID: b.Project.StorageID, DisplayName: "Fixture owner", Role: model.RoleDeveloper, ModelProvider: "codex", Status: model.AgentRegistered, Revision: 1}); err != nil {
				t.Fatal(err)
			}
			if err := r.store.PutRoleBinding(t.Context(), authz.RoleBinding{ID: "role-required-live", PrincipalID: b.Principal, Role: "developer", ScopeID: b.Project.StorageID, BoundBy: "owner", BoundAt: now.Add(-time.Hour), PolicyDigest: "sha256:" + strings.Repeat("a", 64)}); err != nil {
				t.Fatal(err)
			}
			observe := func(ctx context.Context, binding projectcontext.Binding) (store.ProjectContextAuthorityObservation, error) {
				return r.store.ReadProjectContextAuthority(ctx, store.ProjectContextAuthorityRequest{Binding: binding, Purpose: store.ProjectContextAuthorityPlanning}, time.Now().UTC())
			}
			proof := requiredProjectWorkspaceProof{Runtime: r, SQLAuthority: observe, Read: func(ctx context.Context, binding projectcontext.Binding) (requiredProjectWorkspaceState, error) {
				o, err := observe(ctx, binding)
				s := state
				s.SQLAuthorityDigest = o.Digest
				return s, err
			}}
			ctx := context.WithValue(t.Context(), requiredProjectWorkspaceKey{}, proof)
			prepared, err := r.prepareRequiredProjectContext(ctx, authz.Principal{ID: b.Principal}, b, projectcontext.Selection{}, "", 16384)
			if err != nil || prepared.Admission.ExpectedAuthority == nil || prepared.Admission.ExpectedAuthority.Digest == "" {
				t.Fatalf("canonical authority was omitted: %+v %v", prepared.Admission.ExpectedAuthority, err)
			}
			if revoke {
				if err = r.store.RevokeRoleBinding(ctx, "role-required-live", time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
			}
			prepared.Admission.ID = "CTX-required-canonical-join"
			prepared.Admission.Now = time.Now().UTC()
			receipt, err := r.store.CommitProjectContextAdmission(ctx, prepared.Admission)
			if revoke {
				if err == nil || receipt.ID != "" {
					t.Fatal("revoked authority created a qualified receipt")
				}
			} else if err != nil || receipt.Outcome != "QUALIFIED" {
				t.Fatal("current authority was refused", err)
			}
		})
	}
}

func TestRequiredContextInvocationIsOneShotAndCannotBeRestoredFromReceipt(t *testing.T) {
	r, b, record, state := requiredContextFixture(t)
	ctx := context.WithValue(t.Context(), requiredProjectWorkspaceKey{}, requiredProjectWorkspaceProof{Runtime: r, Read: func(context.Context, projectcontext.Binding) (requiredProjectWorkspaceState, error) {
		return state, nil
	}})
	token, err := r.admitRequiredProjectInvocation(ctx, authz.Principal{ID: b.Principal}, b, projectcontext.Selection{}, "Full planning request\n", 16384, "planning", "")
	if err != nil {
		t.Fatal(err)
	}
	text, receipt, err := r.consumeRequiredProjectInvocation(ctx, token)
	if err != nil || receipt.ParentReceiptID != token.receipt.ID || receipt.Outcome != "QUALIFIED" || !strings.Contains(text, record.Body) {
		t.Fatal("complete exact context did not enter admitted invocation", receipt, err)
	}
	if _, _, err = r.consumeRequiredProjectInvocation(ctx, token); err == nil {
		t.Fatal("one-shot context allowed a second invocation")
	}
	raw, err := json.Marshal(token)
	if err != nil || string(raw) != "{}" {
		t.Fatal("live authority serialized", string(raw), err)
	}
	var restored requiredProjectInvocation
	if err = json.Unmarshal([]byte(`{"runtime":{},"consumed":false,"receipt":{"outcome":"QUALIFIED"}}`), &restored); err != nil {
		t.Fatal(err)
	}
	if _, _, err = r.consumeRequiredProjectInvocation(ctx, &restored); err == nil {
		t.Fatal("JSON minted invocation authority")
	}
	claimed := &requiredProjectInvocation{runtime: r, receipt: token.receipt}
	if _, _, err = r.consumeRequiredProjectInvocation(ctx, claimed); err == nil {
		t.Fatal("persisted receipt alone minted another invocation")
	}
}

func TestRequiredContextInvocationOwnedQualificationFailureSpendsToken(t *testing.T) {
	for _, failure := range []string{"expired", "malformed outcome", "foreign runtime"} {
		t.Run(failure, func(t *testing.T) {
			r, b, _, state := requiredContextFixture(t)
			ctx := context.WithValue(t.Context(), requiredProjectWorkspaceKey{}, requiredProjectWorkspaceProof{Runtime: r, Read: func(context.Context, projectcontext.Binding) (requiredProjectWorkspaceState, error) {
				return state, nil
			}})
			token, err := r.admitRequiredProjectInvocation(ctx, authz.Principal{ID: b.Principal}, b, projectcontext.Selection{}, "", 16384, "planning", "")
			if err != nil {
				t.Fatal(err)
			}
			original := token.receipt
			invoker := r
			// Host-side guard invalidation, not a public ability to alter opaque fields.
			switch failure {
			case "expired":
				past := time.Now().UTC().Add(-time.Second)
				token.receipt.ValidUntil = &past
			case "malformed outcome":
				token.receipt.Outcome = "UNKNOWN"
			case "foreign runtime":
				invoker = &Runtime{}
			}
			if text, _, err := invoker.consumeRequiredProjectInvocation(ctx, token); err == nil || text != "" {
				t.Fatal("invalid token reached effect", err)
			}
			if failure == "foreign runtime" {
				if token.consumed {
					t.Fatal("foreign runtime spent rightful owner's token")
				}
				if _, _, err = r.consumeRequiredProjectInvocation(ctx, token); err != nil {
					t.Fatal("foreign refusal denied rightful owner", err)
				}
			} else {
				if !token.consumed {
					t.Fatal("owned qualification failure left token reusable")
				}
				token.receipt = original
				if _, _, err = r.consumeRequiredProjectInvocation(ctx, token); err == nil {
					t.Fatal("restored receipt revived spent token")
				}
			}
		})
	}
}

func TestRequiredContextInvocationRechecksAuthorityMemoryAndExactPacketBeforeEffect(t *testing.T) {
	for _, scenario := range []string{"authority_changed", "memory_acl_revoked", "packet_modified", "cancelled", "foreign_runtime"} {
		t.Run(scenario, func(t *testing.T) {
			r, b, record, state := requiredContextFixture(t)
			ctx := context.WithValue(t.Context(), requiredProjectWorkspaceKey{}, requiredProjectWorkspaceProof{Runtime: r, Read: func(context.Context, projectcontext.Binding) (requiredProjectWorkspaceState, error) {
				return state, nil
			}})
			token, err := r.admitRequiredProjectInvocation(ctx, authz.Principal{ID: b.Principal}, b, projectcontext.Selection{}, "", 16384, "planning", "")
			if err != nil {
				t.Fatal(err)
			}
			invoker := r
			switch scenario {
			case "authority_changed":
				state.AuthorityDigest = "changed-generation"
			case "memory_acl_revoked":
				if _, err = r.store.UpdateMemory(t.Context(), record.ProjectID, record.ID, record.Revision, func(m *model.MemoryRecordV2) error { m.ACLScope = "foreign"; return nil }); err != nil {
					t.Fatal(err)
				}
			case "packet_modified":
				token.prepared.Packet.Text = "omitted essential"
			case "cancelled":
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			case "foreign_runtime":
				invoker = &Runtime{}
			}
			if text, _, err := invoker.consumeRequiredProjectInvocation(ctx, token); err == nil || text != "" {
				t.Fatal("changed invocation reached provider request", text, err)
			}
			if scenario != "foreign_runtime" && !token.consumed {
				t.Fatal("failed invocation token remained reusable")
			}
		})
	}
}
