//go:build governed || !linux

package app

import (
	"context"
	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/model"
)

func privateAGYEnabled() bool                                     { return false }
func privateHarnessProbePreflight(context.Context, string) error  { return nil }
func privateHarnessProbeWorktree(string, string) error            { return nil }
func privateAGYCanonical(name string) string                      { return name }
func privateAGYEndpoint(string) (string, bool)                    { return "", false }
func privateAGYCredential(string) bool                            { return false }
func privateAGYAuthBinds(string, string) ([]model.Bind, error)    { return nil, nil }
func markHarnessVerification(ctx context.Context) context.Context { return ctx }
func isHarnessVerification(context.Context) bool                  { return false }

func validatePrivateAGYModel(string, string) error { return nil }
func privateAGYAdapter(context.Context, string, adapter.ProcessRunner) (adapter.Adapter, error) {
	return nil, model.ErrUnavailable
}

func privateAGYAdditionalEndpoints(string) []string { return nil }
