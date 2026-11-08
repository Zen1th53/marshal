//go:build governed

package app

import (
	"context"
	"errors"

	"github.com/Zen1th53/marshal/internal/permission"
)

func (r *Runtime) checkBoundCredentialSource(_ context.Context, req permission.Request, _ bool) error {
	if req.SourceLaunch != "" || req.SourceOccurrence != "" {
		return errors.New("unexpected model decision source")
	}
	return nil
}
