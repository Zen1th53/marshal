package protocol

import (
	"context"
	"errors"
	"testing"
	"time"
)

type memoryRepository struct {
	stored      Handoff
	createCalls int
}

func (r *memoryRepository) Create(_ context.Context, handoff Handoff) (Handoff, error) {
	r.createCalls++
	r.stored = handoff
	return handoff, nil
}

func (r *memoryRepository) Transition(_ context.Context, id HandoffID, from, to Status, _ Principal) (Handoff, error) {
	if r.stored.ID != id || r.stored.Status != from {
		return Handoff{}, ErrTransitionInvalid
	}
	r.stored.Status = to
	if to == StatusConsumed {
		consumed := time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC)
		r.stored.ConsumedAt = &consumed
	}
	return r.stored, nil
}

func (r *memoryRepository) GetHandoff(_ context.Context, id HandoffID) (Handoff, error) {
	if r.stored.ID != id {
		return Handoff{}, ErrHandoffNotFound
	}
	return r.stored, nil
}

func (r *memoryRepository) EvidenceBelongsToTask(_ context.Context, taskID TaskID, evidenceIDs []EvidenceID) error {
	if taskID != "TASK-123" || len(evidenceIDs) != 1 || evidenceIDs[0] != "EVIDENCE-123" {
		return ErrEvidenceInvalid
	}
	return nil
}

type allowAuthorizer struct{}

func (allowAuthorizer) Authorize(_ context.Context, action Action, principal Principal, handoff Handoff) (AuthorizationDecision, error) {
	if action != ActionCreate || principal.ID != "AGENT-developer" || handoff.TaskID != "TASK-123" {
		return AuthorizationDecision{Allowed: false, Reason: ReasonSenderForged}, ErrSenderForged
	}
	return AuthorizationDecision{Allowed: true, Reason: ReasonAccepted, FreshUntil: time.Date(2026, 8, 18, 0, 0, 0, 0, time.UTC)}, nil
}

type allActionsAuthorizer struct{}

func (allActionsAuthorizer) Authorize(_ context.Context, action Action, principal Principal, _ Handoff) (AuthorizationDecision, error) {
	if action != ActionCreate && action != ActionConsume {
		return AuthorizationDecision{Allowed: false}, ErrAuthorization
	}
	return AuthorizationDecision{Allowed: principal.ID != "", Reason: ReasonAccepted, FreshUntil: time.Date(2026, 8, 18, 0, 0, 0, 0, time.UTC)}, nil
}

func validSubmission() Submission {
	return Submission{
		IdempotencyKey: "handoff-123",
		Handoff: Handoff{
			ID:            "HANDOFF-123",
			Version:       Version1,
			TaskID:        "TASK-123",
			FromAgent:     "AGENT-developer",
			ToRole:        RoleQA,
			Claims:        map[string]string{"summary": "implementation complete"},
			EvidenceIDs:   []EvidenceID{"EVIDENCE-123"},
			ChangedFiles:  []string{"./internal/protocol/engine.go", "internal/protocol/types.go"},
			ContextDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
	}
}

func TestSubmitBindsAuthenticatedSenderAndNormalizesChangedFiles(t *testing.T) {
	repository := &memoryRepository{}
	service := NewService(Config{
		RepositoryRoot: "/repo",
		Now:            func() time.Time { return time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC) },
	}, repository, allowAuthorizer{})

	got, err := service.Submit(context.Background(), Principal{ID: "AGENT-developer", Role: RoleDeveloper}, validSubmission())
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if got.Status != StatusAccepted {
		t.Fatalf("status = %q, want %q", got.Status, StatusAccepted)
	}
	if got.FromAgent != "AGENT-developer" {
		t.Fatalf("sender = %q, want authenticated sender", got.FromAgent)
	}
	if got.ChangedFiles[0] != "internal/protocol/engine.go" || got.ChangedFiles[1] != "internal/protocol/types.go" {
		t.Fatalf("changed files = %#v, want repo-relative normalized paths", got.ChangedFiles)
	}
	if repository.stored.Status != StatusAccepted {
		t.Fatalf("stored status = %q, want accepted", repository.stored.Status)
	}
}

func TestSubmitRejectsForgedSenderBeforePersistence(t *testing.T) {
	repository := &memoryRepository{}
	service := NewService(Config{}, repository, allowAuthorizer{})
	submission := validSubmission()
	submission.Handoff.FromAgent = "AGENT-impersonated"
	_, err := service.Submit(context.Background(), Principal{ID: "AGENT-developer", Role: RoleDeveloper}, submission)
	if !errors.Is(err, ErrSenderForged) {
		t.Fatalf("Submit error = %v, want ErrSenderForged", err)
	}
	if repository.createCalls != 0 {
		t.Fatalf("persistence calls = %d, want zero", repository.createCalls)
	}
}

func TestSubmitRejectsUnknownVersionBeforePersistence(t *testing.T) {
	repository := &memoryRepository{}
	service := NewService(Config{}, repository, allowAuthorizer{})
	submission := validSubmission()
	submission.Handoff.Version = Version1 + 1
	_, err := service.Submit(context.Background(), Principal{ID: "AGENT-developer", Role: RoleDeveloper}, submission)
	if !errors.Is(err, ErrVersionUnsupported) {
		t.Fatalf("Submit error = %v, want ErrVersionUnsupported", err)
	}
	if repository.createCalls != 0 {
		t.Fatalf("persistence calls = %d, want zero", repository.createCalls)
	}
}

func TestHandoffRejectsClaimsThatAttemptToTransferAuthorityOrSecrets(t *testing.T) {
	handoff := validSubmission().Handoff
	handoff.Status = StatusCreated
	handoff.IdempotencyKey = "handoff-123"
	handoff.CreatedAt = time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC)
	for key := range map[string]string{"capabilities": "git.push", "secret": "MARSHAL_TEST_SECRET_T28_A01"} {
		handoff.Claims = map[string]string{key: "value"}
		if !errors.Is(handoff.Validate(), ErrAuthorityTransfer) {
			t.Fatalf("claim %q was accepted", key)
		}
	}
}

func TestConsumeRequiresIndependentRecipientAuthority(t *testing.T) {
	repository := &memoryRepository{}
	service := NewService(Config{Now: func() time.Time { return time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC) }}, repository, allActionsAuthorizer{})
	accepted, err := service.Submit(context.Background(), Principal{ID: "AGENT-developer", Role: RoleDeveloper}, validSubmission())
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	consumed, err := service.Consume(context.Background(), Principal{ID: "AGENT-qa", Role: RoleQA}, accepted.ID)
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if consumed.Status != StatusConsumed || consumed.ConsumedAt == nil {
		t.Fatalf("consumed handoff = %#v, want durable consumed state", consumed)
	}
}

func TestA08SubmitReplayReturnsOneAcceptedSemanticResult(t *testing.T) {
	repository := &memoryRepository{}
	service := NewService(Config{Now: func() time.Time { return time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC) }}, repository, allActionsAuthorizer{})
	principal := Principal{ID: "AGENT-developer", Role: RoleDeveloper}
	first, err := service.Submit(context.Background(), principal, validSubmission())
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Submit(context.Background(), principal, validSubmission())
	if err != nil {
		t.Fatalf("replay Submit: %v", err)
	}
	if second.Status != StatusAccepted || second.ID != first.ID || repository.createCalls != 2 {
		t.Fatalf("replay = %#v create calls=%d, want original accepted semantic result", second, repository.createCalls)
	}
}
