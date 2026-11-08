//go:build !governed && linux

package app

import (
	"context"
	"errors"
	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/adapter/antigravity"
	"os"
	"path/filepath"
	"regexp"

	"github.com/Zen1th53/marshal/internal/model"
)

// Native AGY qualification is private/Linux only. Discovery and account state
// remain distinct from a successful governed provider profile.
func privateAGYEnabled() bool { return true }

// VerifyMarshalWorker owns this disposable worktree. An empty parent permits
// the existing sealed configuration rules without importing repository/host
// MCP settings or creating an untracked file. Other execution paths retain
// their normal workspace requirements.
func privateHarnessProbeWorktree(provider, worktree string) error {
	if provider != "agy" {
		return nil
	}
	path := filepath.Join(worktree, ".agents")
	if err := os.Mkdir(path, 0700); err != nil && !os.IsExist(err) {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return model.ErrInvalid
	}
	return nil
}
func privateAGYCanonical(name string) string {
	if name == "antigravity" {
		return "agy"
	}
	return name
}
func privateAGYEndpoint(name string) (string, bool) {
	if privateAGYCanonical(name) == "agy" {
		return "daily-cloudcode-pa.googleapis.com:443", true
	}
	return "", false
}
func privateAGYAdditionalEndpoints(name string) []string {
	if name == "agy" {
		return []string{"www.googleapis.com:443"}
	}
	return nil
}
func privateAGYCredential(name string) bool { return name == "agy" }
func privateAGYAuthBinds(name, home string) ([]model.Bind, error) {
	if name != "agy" {
		return nil, nil
	}
	source := filepath.Join(home, ".gemini", "antigravity-cli", "antigravity-oauth-token")
	info, err := os.Lstat(source)
	if err != nil || !info.Mode().IsRegular() {
		return nil, model.ErrUnavailable
	}
	return []model.Bind{{Source: source, Target: "/home/marshal/.gemini/antigravity-cli/antigravity-oauth-token"}}, nil
}

// A typed marker identifies actual Runtime.VerifyMarshalWorker composition;
// worker text or PROBE-looking task identifiers cannot select the probe path.
type harnessVerificationContextKey struct{}

func markHarnessVerification(ctx context.Context) context.Context {
	return context.WithValue(ctx, harnessVerificationContextKey{}, true)
}
func isHarnessVerification(ctx context.Context) bool {
	v, _ := ctx.Value(harnessVerificationContextKey{}).(bool)
	return v
}

var agySafeModel = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

func validatePrivateAGYModel(provider, modelName string) error {
	if provider == "agy" && !agySafeModel.MatchString(modelName) {
		return errors.New("AGY harness requires an explicit safe native model selector")
	}
	return nil
}
func privateAGYAdapter(ctx context.Context, binary string, runner adapter.ProcessRunner) (adapter.Adapter, error) {
	if isHarnessVerification(ctx) {
		return antigravity.NewGovernedProbe(binary, runner), nil
	}
	return antigravity.NewGoverned(binary, runner), nil
}
