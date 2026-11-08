//go:build !governed

package app

import (
	"context"
	"testing"

	"github.com/Zen1th53/marshal/internal/projectid"
)

func architectureServerContext(t *testing.T, r *Runtime, cli *MarshalCLI) context.Context {
	t.Helper()
	r, b, _, _ := requiredContextFixtureForRuntime(t, r, true)
	request := planCreateRequest()
	request.SessionID, request.ProjectID = b.Session, projectid.ID(r.ProjectIdentity())
	if _, err := r.Plans().Create(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	control, err := r.OpenLocalControl(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if cli != nil {
		cli.runtime = r
		cli.ProjectID = r.ProjectIdentity()
		cli.Dir = r.ProjectRoot()
		cli.requiredGoalSession = b.Session
	}
	return control.Context(t.Context())
}
