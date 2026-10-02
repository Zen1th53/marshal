//go:build linux

package tui

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/artifact"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/project"
)

// The real binary lists a stored artifact, re-checks its bytes, and reports a
// tampered payload instead of presenting it as evidence.
func TestPTYEvidenceListAndDetail(t *testing.T) {
	bin := buildMarshalBinary(t)
	sweepWorkEnvironment(t)
	t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1")
	projectDir := initProject(t, bin)
	t.Chdir(projectDir)
	ctx := context.Background()
	runtime, err := app.Open(ctx, projectDir)
	if err != nil {
		t.Fatal(err)
	}
	layout, err := project.Discover(projectDir)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := artifact.New(layout.Artifacts, runtime.Store()).Put(ctx, model.ArtifactInput{
		ProjectID: runtime.ProjectID(), Kind: "report", SourceCommit: "abc123", Data: strings.NewReader("pty evidence")})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}

	terminal := startFrozenTUIInProject(t, 40, 180, bin, projectDir, "tui", "SESSION-pty-evidence")
	terminal.mustSee("MARSHAL")
	terminal.sendLine("/evidence list")
	terminal.mustSee("Stored artifacts (1)")
	terminal.mustSee(stored.ID)
	terminal.sendLine("/evidence " + stored.ID)
	terminal.mustSee("Payload:  VERIFIED")

	if err := os.Chmod(stored.Path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stored.Path, []byte("forged"), 0o600); err != nil {
		t.Fatal(err)
	}
	terminal.sendLine("/evidence " + stored.ID)
	terminal.mustSee("DIGEST_MISMATCH")
}
