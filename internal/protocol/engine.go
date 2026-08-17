package protocol

import (
	"context"
	"path/filepath"
	"strings"
	"time"
)

type Config struct {
	RepositoryRoot string
	Now            func() time.Time
}

type Service struct {
	config     Config
	repository Repository
	authorizer Authorizer
}

func NewService(config Config, repository Repository, authorizer Authorizer) *Service {
	if config.Now == nil {
		config.Now = func() time.Time { return time.Now().UTC() }
	}
	return &Service{config: config, repository: repository, authorizer: authorizer}
}

func (s *Service) Submit(ctx context.Context, principal Principal, submission Submission) (Handoff, error) {
	if s == nil || s.repository == nil || s.authorizer == nil {
		return Handoff{}, ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return Handoff{}, err
	}
	handoff := cloneHandoff(submission.Handoff)
	if handoff.FromAgent != principal.ID || !validID(principal.ID) || !validRole(principal.Role) {
		return Handoff{}, ErrSenderForged
	}
	handoff.IdempotencyKey = submission.IdempotencyKey
	handoff.Status = StatusCreated
	handoff.CreatedAt = s.config.Now().UTC()
	handoff.ConsumedAt = nil
	files, err := normalizeChangedFiles(handoff.ChangedFiles)
	if err != nil {
		return Handoff{}, err
	}
	handoff.ChangedFiles = files
	if err := handoff.Validate(); err != nil {
		return Handoff{}, err
	}
	if err := s.repository.EvidenceBelongsToTask(ctx, handoff.TaskID, handoff.EvidenceIDs); err != nil {
		return Handoff{}, ErrEvidenceInvalid
	}
	decision, err := s.authorizer.Authorize(ctx, ActionCreate, principal, cloneHandoff(handoff))
	if err != nil || !decision.Allowed || !decision.FreshUntil.After(s.config.Now().UTC()) {
		return Handoff{}, ErrAuthorization
	}
	created, err := s.repository.Create(ctx, handoff)
	if err != nil {
		return Handoff{}, err
	}
	validated, err := s.repository.Transition(ctx, created.ID, StatusCreated, StatusValidated, principal)
	if err != nil {
		return Handoff{}, err
	}
	accepted, err := s.repository.Transition(ctx, validated.ID, StatusValidated, StatusAccepted, principal)
	if err != nil {
		return Handoff{}, err
	}
	return cloneHandoff(accepted), nil
}

func normalizeChangedFiles(files []string) ([]string, error) {
	normalized := make([]string, len(files))
	seen := make(map[string]struct{}, len(files))
	for index, file := range files {
		if file == "" || filepath.IsAbs(file) {
			return nil, ErrForeignTask
		}
		clean := filepath.ToSlash(filepath.Clean(file))
		if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || !validText(clean) {
			return nil, ErrForeignTask
		}
		if _, exists := seen[clean]; exists {
			return nil, ErrInvalid
		}
		seen[clean] = struct{}{}
		normalized[index] = clean
	}
	return normalized, nil
}
