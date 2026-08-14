package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/evidence"
	"github.com/Zen1th53/marshal/internal/model"
)

type a05AllowingAuthorizer struct{}

func (a05AllowingAuthorizer) Authorize(_ context.Context, request evidence.AccessRequest) (evidence.AuthorizationDecision, error) {
	return evidence.AuthorizationDecision{
		Allowed: true, SubjectID: request.SubjectID, TaskID: request.TaskID,
		ChangeID: request.ChangeID, NodeID: request.NodeID, State: request.CurrentState,
		PolicyDigest: "sha256:" + strings.Repeat("a", 64), FreshUntil: time.Now().UTC().Add(time.Minute),
	}, nil
}

func TestEvidencePutNodeAppendsStructuredAuditFact(t *testing.T) {
	st := openEvidenceStoreWithSecurity(t, evidence.NewStrictSanitizer(evidence.SanitizerConfig{}), a05AllowingAuthorizer{})
	node := testEvidenceNode("EVIDENCE-A05-NODE", "claim", "audit")
	if _, err := st.PutNode(context.Background(), node); err != nil {
		t.Fatalf("PutNode: %v", err)
	}

	events, err := st.ListEvents(context.Background())
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	var found bool
	for _, event := range events {
		if event.Type != "evidence.node.stored" {
			continue
		}
		found = true
		if got := event.Data["evidence_id"]; got != string(node.ID) {
			t.Fatalf("evidence_id = %#v, want %q", got, node.ID)
		}
		if got := event.Data["content_digest"]; got != node.Digest {
			t.Fatalf("content_digest = %#v, want %q", got, node.Digest)
		}
		if got := event.Data["action"]; got != string(evidence.ActionCreate) {
			t.Fatalf("action = %#v, want %q", got, evidence.ActionCreate)
		}
	}
	if !found {
		t.Fatalf("no evidence.node.stored audit fact in %#v", events)
	}
}

func TestEvidenceDuplicateNodeHasOneSemanticSuccessAuditFact(t *testing.T) {
	st := openEvidenceStoreWithSecurity(t, evidence.NewStrictSanitizer(evidence.SanitizerConfig{}), allowingAuthorizer{})
	node := testEvidenceNode("EVIDENCE-A05-IDEMPOTENT", "claim", "same")
	for i := 0; i < 2; i++ {
		if _, err := st.PutNode(context.Background(), node); err != nil {
			t.Fatalf("PutNode attempt %d: %v", i+1, err)
		}
	}
	events, err := st.ListEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var successes int
	for _, event := range events {
		if event.Type == "evidence.node.stored" {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("semantic success facts = %d, want 1; events = %#v", successes, events)
	}
}

func TestEvidenceLinkAppendsStructuredAuditFact(t *testing.T) {
	st := openEvidenceStoreWithSecurity(t, evidence.NewStrictSanitizer(evidence.SanitizerConfig{}), allowingAuthorizer{})
	from := testEvidenceNode("EVIDENCE-A05-FROM", "claim", "from")
	to := testEvidenceNode("EVIDENCE-A05-TO", "claim", "to")
	for _, node := range []evidence.Node{from, to} {
		if _, err := st.PutNode(context.Background(), node); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.Link(context.Background(), evidence.Edge{From: from.ID, To: to.ID, Relation: "derived-from"}); err != nil {
		t.Fatalf("Link: %v", err)
	}
	events, err := st.ListEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, event := range events {
		if event.Type != "evidence.edge.linked" {
			continue
		}
		found = true
		if got := event.Data["from_evidence_id"]; got != string(from.ID) {
			t.Fatalf("from_evidence_id = %#v, want %q", got, from.ID)
		}
		if got := event.Data["to_evidence_id"]; got != string(to.ID) {
			t.Fatalf("to_evidence_id = %#v, want %q", got, to.ID)
		}
		if got := event.Data["relation"]; got != "derived-from" {
			t.Fatalf("relation = %#v, want derived-from", got)
		}
	}
	if !found {
		t.Fatalf("no evidence.edge.linked audit fact in %#v", events)
	}
}

func TestEvidenceAuthorizedTransitionAppendsCorrelationAndRawPathRemainsDenied(t *testing.T) {
	st := openEvidenceStoreWithSecurity(t, evidence.NewStrictSanitizer(evidence.SanitizerConfig{}), a05AllowingAuthorizer{})
	node := testEvidenceNode("EVIDENCE-A05-TRANSITION", "claim", "transition")
	if _, err := st.PutNode(context.Background(), node); err != nil {
		t.Fatal(err)
	}
	request := evidence.AccessRequest{
		SubjectID: "subject-a05", TaskID: "task-a05", ChangeID: "change-a05",
		NodeID: node.ID, Action: evidence.ActionTransition, TargetState: evidence.StateLinked,
	}
	if err := st.TransitionNodeAuthorized(context.Background(), request); err != nil {
		t.Fatalf("authorized transition: %v", err)
	}
	if err := st.TransitionNode(context.Background(), node.ID, evidence.StateArchived); !errors.Is(err, evidence.ErrAuthorizationUnavailable) {
		t.Fatalf("raw transition error = %v, want %v", err, evidence.ErrAuthorizationUnavailable)
	}
	events, err := st.ListEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, event := range events {
		if event.Type != "evidence.state.transitioned" {
			continue
		}
		found = true
		for key, want := range map[string]string{
			"evidence_id": string(node.ID), "subject_id": request.SubjectID,
			"task_id": request.TaskID, "change_id": request.ChangeID,
			"previous_state": string(evidence.StateStored), "new_state": string(evidence.StateLinked),
		} {
			if got := event.Data[key]; got != want {
				t.Fatalf("%s = %#v, want %q", key, got, want)
			}
		}
	}
	if !found {
		t.Fatalf("no evidence.state.transitioned audit fact in %#v", events)
	}
}

func TestEvidenceAuditNeverPersistsSecretMarker(t *testing.T) {
	const marker = "MARSHAL_TEST_SECRET_T06_A05_7f9d"
	st := openEvidenceStoreWithSecurity(t, evidence.NewStrictSanitizer(evidence.SanitizerConfig{LiteralSecrets: []string{marker}}), allowingAuthorizer{})
	node := testEvidenceNode("EVIDENCE-A05-SECRET", "claim", marker)
	if _, err := st.PutNode(context.Background(), node); !errors.Is(err, evidence.ErrSecretRejected) {
		t.Fatalf("PutNode error = %v, want %v", err, evidence.ErrSecretRejected)
	}
	events, err := st.ListEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), marker) {
		t.Fatalf("secret marker appeared in audit events")
	}
	var raw string
	if err := st.db.QueryRow(`SELECT coalesce(group_concat(data_json), '') FROM audit_events`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, marker) {
		t.Fatalf("secret marker persisted in audit_events: %q", raw)
	}
}

func TestEvidenceDigestMismatchAppendsSafeAuditFact(t *testing.T) {
	st := openEvidenceStore(t, evidence.NewStrictSanitizer(evidence.SanitizerConfig{}))
	node := testEvidenceNode("EVIDENCE-A05-DIGEST", "claim", "digest")
	node.Digest = "sha256:" + strings.Repeat("f", 64)
	if _, err := st.PutNode(context.Background(), node); !errors.Is(err, evidence.ErrDigestMismatch) {
		t.Fatalf("PutNode error = %v, want digest mismatch", err)
	}
	events, err := st.ListEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, event := range events {
		if event.Type == "evidence.digest.mismatch" {
			found = true
			if event.Data["evidence_id"] != string(node.ID) || event.Data["reason_code"] != string(evidence.CodeDigestMismatch) {
				t.Fatalf("digest event = %#v", event.Data)
			}
		}
	}
	if !found {
		t.Fatal("missing evidence.digest.mismatch audit fact")
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM evidence_nodes"); got != 0 {
		t.Fatalf("evidence rows = %d, want 0", got)
	}
}

func TestEvidenceAuditRejectsOversizedCorrelationBeforeCommit(t *testing.T) {
	st := openEvidenceStore(t, evidence.NewStrictSanitizer(evidence.SanitizerConfig{}))
	node := testEvidenceNode("EVIDENCE-"+strings.Repeat("x", maxEvidenceAuditValue), "claim", "bounded")
	if _, err := st.PutNode(context.Background(), node); err == nil {
		t.Fatal("oversized evidence correlation was accepted")
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM evidence_nodes"); got != 0 {
		t.Fatalf("evidence rows = %d, want 0", got)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM audit_events"); got != 0 {
		t.Fatalf("audit rows = %d, want 0", got)
	}
}

func TestEvidenceAuditDoesNotExposeAuthorizationErrorSecret(t *testing.T) {
	const marker = "MARSHAL_TEST_SECRET_T06_A05_backend_9d2c"
	st := openEvidenceStoreWithSecurity(t, evidence.NewStrictSanitizer(evidence.SanitizerConfig{}), fixedAuthorizer{err: errors.New(marker)})
	node := testEvidenceNode("EVIDENCE-A05-AUTHZ-SECRET", "claim", "authz")
	if _, err := st.PutNode(context.Background(), node); err != nil {
		t.Fatal(err)
	}
	err := st.TransitionNodeAuthorized(context.Background(), evidence.AccessRequest{
		SubjectID: "subject", NodeID: node.ID, Action: evidence.ActionTransition, TargetState: evidence.StateLinked,
	})
	if !errors.Is(err, evidence.ErrAuthorizationUnavailable) || strings.Contains(err.Error(), marker) {
		t.Fatalf("authorization error = %v, marker leaked", err)
	}
	events, err := st.ListEvents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), marker) {
		t.Fatal("authorization error marker appeared in audit events")
	}
}

func TestEvidenceAuditRejectsNestedProviderPayloadBeforePersistence(t *testing.T) {
	const marker = "MARSHAL_TEST_SECRET_T06_A07_AUDIT_4c8e"
	st := openEvidenceStore(t, evidence.NewStrictSanitizer(evidence.SanitizerConfig{}))
	err := st.recordEvidenceEvent(context.Background(), "evidence.test.rejected", map[string]any{
		"provider_error": map[string]any{"detail": marker},
	})
	if !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("recordEvidenceEvent error = %v, want invalid audit payload", err)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM audit_events"); got != 0 {
		t.Fatalf("audit rows = %d, want 0", got)
	}
	if events, err := st.ListEvents(context.Background()); err != nil {
		t.Fatal(err)
	} else if encoded, err := json.Marshal(events); err != nil {
		t.Fatal(err)
	} else if strings.Contains(string(encoded), marker) {
		t.Fatal("rejected audit marker appeared in events")
	}
}
