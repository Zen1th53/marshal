package protocol

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type rejectingEvidenceRepository struct{ memoryRepository }

func (r *rejectingEvidenceRepository) EvidenceBelongsToTask(context.Context, TaskID, []EvidenceID) error {
	return ErrEvidenceInvalid
}

type staleAuthorizer struct{}

func (staleAuthorizer) Authorize(context.Context, Action, Principal, Handoff) (AuthorizationDecision, error) {
	return AuthorizationDecision{Allowed: true, FreshUntil: time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC)}, nil
}

func TestA07ForeignEvidenceIsDeniedBeforePersistence(t *testing.T) {
	repository := &rejectingEvidenceRepository{}
	service := NewService(Config{}, repository, allowAuthorizer{})
	_, err := service.Submit(context.Background(), Principal{ID: "AGENT-developer", Role: RoleDeveloper}, validSubmission())
	if !errors.Is(err, ErrEvidenceInvalid) {
		t.Fatalf("Submit error = %v, want ErrEvidenceInvalid", err)
	}
	if repository.createCalls != 0 {
		t.Fatalf("persistence calls = %d, want zero", repository.createCalls)
	}
}

func TestA07TraversalAndOversizedPayloadAreDeniedBeforePersistence(t *testing.T) {
	for name, mutate := range map[string]func(*Submission){
		"traversal": func(submission *Submission) { submission.Handoff.ChangedFiles = []string{"../../outside"} },
		"oversized": func(submission *Submission) {
			submission.Handoff.Claims = make(map[string]string, maxClaims+1)
			for index := 0; index <= maxClaims; index++ {
				submission.Handoff.Claims[string(rune('a'+index))] = "value"
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			repository := &memoryRepository{}
			service := NewService(Config{}, repository, allowAuthorizer{})
			submission := validSubmission()
			mutate(&submission)
			_, err := service.Submit(context.Background(), Principal{ID: "AGENT-developer", Role: RoleDeveloper}, submission)
			if err == nil {
				t.Fatal("Submit accepted malformed handoff")
			}
			if repository.createCalls != 0 {
				t.Fatalf("persistence calls = %d, want zero", repository.createCalls)
			}
		})
	}
}

func TestA07StaleAuthorityAndSecretMarkerFailClosedWithoutLeak(t *testing.T) {
	repository := &memoryRepository{}
	service := NewService(Config{Now: func() time.Time { return time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC) }}, repository, staleAuthorizer{})
	_, err := service.Submit(context.Background(), Principal{ID: "AGENT-developer", Role: RoleDeveloper}, validSubmission())
	if !errors.Is(err, ErrAuthorization) || repository.createCalls != 0 {
		t.Fatalf("stale result err=%v persistence=%d", err, repository.createCalls)
	}
	marker := "MARSHAL_TEST_SECRET_T28_A07"
	handoff := validSubmission().Handoff
	handoff.Status, handoff.IdempotencyKey, handoff.CreatedAt = StatusCreated, "secret-marker", time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC)
	handoff.Claims = map[string]string{"secret": marker}
	err = handoff.Validate()
	if !errors.Is(err, ErrAuthorityTransfer) || strings.Contains(err.Error(), marker) {
		t.Fatalf("secret validation error=%v leaked=%t", err, strings.Contains(err.Error(), marker))
	}
}

func TestA07NilServiceFailsClosedWithoutPanic(t *testing.T) {
	var service *Service
	if _, err := service.Submit(context.Background(), Principal{}, Submission{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil service error = %v, want ErrUnavailable", err)
	}
}

func FuzzA07NormalizeChangedFiles(f *testing.F) {
	for _, seed := range []string{"internal/protocol/engine.go", "./a", "../../outside", "/absolute", "a/../b", ""} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		files, err := normalizeChangedFiles([]string{input})
		if err != nil {
			return
		}
		if len(files) != 1 || files[0] == "." || files[0] == ".." || strings.HasPrefix(files[0], "../") || strings.HasPrefix(files[0], "/") {
			t.Fatalf("unsafe normalized path %q from %q", files, input)
		}
	})
}
