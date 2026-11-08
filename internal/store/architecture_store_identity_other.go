//go:build !linux

package store

import (
	"context"
	"database/sql"
	"github.com/Zen1th53/marshal/internal/model"
)

type architectureSQLiteFile struct{}

func architectureSQLiteDSN(path string) (string, *architectureSQLiteFile, error) {
	return path, nil, nil
}
func (s *Store) validateArchitectureFile(context.Context, string) error { return model.ErrPolicyDenied }
func (s *Store) validateArchitectureTransaction(context.Context, *sql.Tx, string) error {
	return model.ErrPolicyDenied
}
