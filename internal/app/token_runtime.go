package app

import (
	"context"
	"errors"
	"strings"

	"github.com/Zen1th53/marshal/internal/auth"
)

// TokenCreateRequest is the closed application boundary used by local UI
// surfaces. Kind and capabilities are validated by the canonical auth package.
type TokenCreateRequest struct {
	Name           string
	Kind           auth.PrincipalKind
	Capabilities   []string
	IdempotencyKey string
}

type TokenCreateResult struct {
	Record    auth.TokenRecord
	Plaintext string
	Created   bool
}

func (r *Runtime) TokenMetadata(context.Context) ([]auth.TokenRecord, error) {
	if r == nil || r.tokenManager == nil {
		return nil, errors.New("token authority is unavailable")
	}
	records, err := r.tokenManager.ListTokens()
	return auth.RedactTokenMetadata(records), err
}

func (r *Runtime) CreateAccessToken(_ context.Context, req TokenCreateRequest) (TokenCreateResult, error) {
	if r == nil || r.tokenManager == nil {
		return TokenCreateResult{}, errors.New("token authority is unavailable")
	}
	req.Name = strings.TrimSpace(req.Name)
	plaintext, record, created, err := r.tokenManager.CreateTokenIdempotent(
		req.Name, req.Kind, req.Capabilities, req.IdempotencyKey)
	if err != nil {
		return TokenCreateResult{}, err
	}
	return TokenCreateResult{Record: record, Plaintext: plaintext, Created: created}, nil
}

func (r *Runtime) RevokeAccessToken(_ context.Context, id string) error {
	if r == nil || r.tokenManager == nil {
		return errors.New("token authority is unavailable")
	}
	return r.tokenManager.RevokeToken(strings.TrimSpace(id))
}
