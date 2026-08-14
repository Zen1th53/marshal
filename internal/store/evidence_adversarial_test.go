package store

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/Zen1th53/marshal/internal/evidence"
)

func TestEvidenceRejectsAllIllegalLifecycleTransitions(t *testing.T) {
	cases := []struct {
		name   string
		from   evidence.State
		target evidence.State
	}{
		{"stored-to-archived", evidence.StateStored, evidence.StateArchived},
		{"stored-to-exported", evidence.StateStored, evidence.StateExported},
		{"stored-to-draft", evidence.StateStored, evidence.StateDraft},
		{"linked-to-stored", evidence.StateLinked, evidence.StateStored},
		{"linked-to-exported", evidence.StateLinked, evidence.StateExported},
		{"archived-to-stored", evidence.StateArchived, evidence.StateStored},
		{"archived-to-linked", evidence.StateArchived, evidence.StateLinked},
		{"exported-to-stored", evidence.StateExported, evidence.StateStored},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := openEvidenceStoreWithSecurity(t, evidence.NewStrictSanitizer(evidence.SanitizerConfig{}), allowingAuthorizer{})
			node := testEvidenceNode("A07-"+strings.ReplaceAll(tc.name, "-", ""), "claim", tc.name)
			if _, err := st.PutNode(context.Background(), node); err != nil {
				t.Fatal(err)
			}
			if tc.from != evidence.StateStored {
				for state := evidence.StateStored; state != tc.from; {
					next := evidence.StateLinked
					if state == evidence.StateLinked {
						next = evidence.StateArchived
					}
					if err := st.TransitionNodeAuthorized(context.Background(), evidence.AccessRequest{SubjectID: "subject", NodeID: node.ID, Action: evidence.ActionTransition, TargetState: next}); err != nil {
						t.Fatal(err)
					}
					state = next
				}
			}
			err := st.TransitionNodeAuthorized(context.Background(), evidence.AccessRequest{SubjectID: "subject", NodeID: node.ID, Action: evidence.ActionTransition, TargetState: tc.target})
			if !errors.Is(err, evidence.ErrInvalidTransition) {
				t.Fatalf("error = %v, want %v", err, evidence.ErrInvalidTransition)
			}
			got, err := st.Get(context.Background(), node.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.State != tc.from {
				t.Fatalf("state = %s, want %s", got.State, tc.from)
			}
		})
	}
}

func TestConcurrentConflictingTransitionsHaveOneSemanticSuccess(t *testing.T) {
	st := openEvidenceStoreWithSecurity(t, evidence.NewStrictSanitizer(evidence.SanitizerConfig{}), allowingAuthorizer{})
	node := testEvidenceNode("A07-CONFLICT", "claim", "conflict")
	if _, err := st.PutNode(context.Background(), node); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, target := range []evidence.State{evidence.StateLinked, evidence.StateArchived} {
		wg.Add(1)
		go func(target evidence.State) {
			defer wg.Done()
			<-start
			errs <- st.TransitionNodeAuthorized(context.Background(), evidence.AccessRequest{SubjectID: "subject", NodeID: node.ID, Action: evidence.ActionTransition, TargetState: target})
		}(target)
	}
	close(start)
	wg.Wait()
	close(errs)
	successes := 0
	for err := range errs {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successful transitions = %d, want 1", successes)
	}
	got, err := st.Get(context.Background(), node.ID)
	if err != nil || got.State != evidence.StateLinked {
		t.Fatalf("state = %s, err=%v", got.State, err)
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM audit_events WHERE event_type = ?", "evidence.state.transitioned"); got != 1 {
		t.Fatalf("transition audit facts = %d, want 1", got)
	}
}

func TestDuplicateEdgeDoesNotDuplicateAudit(t *testing.T) {
	st := openEvidenceStore(t, evidence.NewStrictSanitizer(evidence.SanitizerConfig{}))
	from := testEvidenceNode("A07-EDGE-FROM", "claim", "from")
	to := testEvidenceNode("A07-EDGE-TO", "claim", "to")
	for _, node := range []evidence.Node{from, to} {
		if _, err := st.PutNode(context.Background(), node); err != nil {
			t.Fatal(err)
		}
	}
	edge := evidence.Edge{From: from.ID, To: to.ID, Relation: "derived-from"}
	for i := 0; i < 2; i++ {
		if _, err := st.Link(context.Background(), edge); err != nil {
			t.Fatal(err)
		}
	}
	if got := queryInt(t, st.db, "SELECT count(*) FROM audit_events WHERE event_type = ?", "evidence.edge.linked"); got != 1 {
		t.Fatalf("edge audit facts = %d, want 1", got)
	}
}
