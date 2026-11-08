//go:build !governed

package app

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/projectcontext"
)

func TestRequiredArchitectureAutomaticProviderSelectionUsesOwnedHostCLI(t *testing.T) {
	r, ctx, cli, envelope, id, captured := architectureRuntimeFixture(t, "complete")
	data, err := os.ReadFile(cli.Binary)
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err = os.WriteFile(filepath.Join(bin, "claude"), data, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	receipt, err := r.runRequiredArchitectureReview(ctx, id, envelope)
	if err != nil || receipt.Outcome != "COMPATIBLE" {
		t.Fatal("automatic reviewer bypassed owned host transport", receipt, err)
	}
	if _, err = os.Stat(captured); err != nil {
		t.Fatal("selected reviewer did not execute owned host", err)
	}
}

func TestRequiredIntegrationSourceRejectsMissingOrContradictoryProcess05Import(t *testing.T) {
	for _, fault := range []string{"missing", "wrong-result", "duplicate"} {
		t.Run(fault, func(t *testing.T) {
			r, ctx, s, id, run, head, root := integrationSourceFixture(t)
			stored, err := r.store.GetMarshalRun(ctx, r.ProjectID(), id)
			if err != nil {
				t.Fatal(err)
			}
			handin, err := r.store.GetMarshalHandIn(ctx, id, "fix", 1)
			if err != nil {
				t.Fatal(err)
			}
			// Existing hand-ins are immutable. Record a new explicit fixture
			// attempt using supported writes, then inspect its actual provenance.
			run.Tasks[0].EvidenceAttemptBase = 1
			switch fault {
			case "missing":
				handin.Value.RuntimeObserved = nil
			case "wrong-result":
				handin.Value.RuntimeObserved[0].Output = "imported " + run.BaseCommit
			case "duplicate":
				handin.Value.RuntimeObserved = append(handin.Value.RuntimeObserved, marshal.CommandRecord{Command: "process05 " + root.RunID, Output: "imported " + root.Tasks["fix"].ResultCommit})
			}
			if _, err = r.store.SetMarshalHandIn(ctx, id, "fix", 2, handin.Value); err != nil {
				t.Fatal(err)
			}
			if _, err = r.store.SetMarshalRun(ctx, r.ProjectID(), id, run, stored.Revision); err != nil {
				t.Fatal(err)
			}
			if err = r.observeRequiredMarshalIntegrationSource(ctx, s, id, run, head); !errors.Is(err, projectcontext.ErrChanged) {
				t.Fatal("unproved or ambiguous actual import gained a trusted source", err)
			}
			if _, _, _, err = r.resolveRequiredCompletionSource(ctx, root); err == nil {
				t.Fatal("rejected import retained a source capability")
			}
		})
	}
}

func TestRequiredArchitectureUnavailableProviderRefusesWithoutSpawning(t *testing.T) {
	r, ctx, _, envelope, id, captured := architectureRuntimeFixture(t, "complete")
	session, err := r.store.GetVerificationSession(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	if _, err = r.runRequiredArchitectureReview(ctx, id, envelope); !errors.Is(err, projectcontext.ErrReviewPending) {
		t.Fatal("missing provider fabricated architecture", err)
	}
	if _, err = r.Verification().completeRequiredContextAttestation(ctx, session, envelope, "host-only"); !errors.Is(err, projectcontext.ErrReviewPending) {
		t.Fatal("missing provider fabricated completion", err)
	}
	if _, err = os.Stat(captured); !os.IsNotExist(err) {
		t.Fatal("unavailable provider spawned host")
	}
}

// An authoritative input larger than the context envelope is never truncated
// or omitted: it is carried in full in the review's read-only input file,
// bound by its sha256, while the prompt keeps a compact view that fits.
func TestRequiredArchitectureOversizedAuthoritativeInputTravelsInItsBoundFile(t *testing.T) {
	r, ctx, cli, envelope, id, captured := architectureRuntimeFixture(t, "complete")
	session, err := r.store.GetVerificationSession(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	oversized := strings.Repeat("bounded-source-observation", 4096)
	session.Version++
	session.Evidence[0].Command = []string{oversized}
	if err = r.store.UpdateVerificationSession(ctx, session, session.Version-1); err != nil {
		t.Fatal(err)
	}
	if _, err = r.runRequiredArchitectureReviewWithCLI(ctx, cli, id, envelope); err != nil {
		t.Fatal("oversized input in its bound file was refused", err)
	}
	prompt, err := os.ReadFile(captured)
	if err != nil {
		t.Fatal("review host process did not run", err)
	}
	if strings.Contains(string(prompt), oversized) || !strings.Contains(string(prompt), architectureInputFileName) || !strings.Contains(string(prompt), `"sha256":"sha256:`) {
		t.Fatal("the prompt does not bind the full input by its file")
	}
}

func TestRequiredIntegrationSourceRejectsSymlinkAndForeignGitAtApprovedPath(t *testing.T) {
	for _, scenario := range []string{"symlink", "foreign-common-git"} {
		t.Run(scenario, func(t *testing.T) {
			r, ctx, s, id, run, head, root := integrationSourceFixture(t)
			if err := r.observeRequiredMarshalIntegrationSource(ctx, s, id, run, head); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(r.layout.Worktrees, integrationTaskID(id, run))
			moved := path + "-retained-original"
			if err := os.Rename(path, moved); err != nil {
				t.Fatal(err)
			}
			if scenario == "symlink" {
				if err := os.Symlink(moved, path); err != nil {
					t.Fatal(err)
				}
			} else {
				process := exec.CommandContext(ctx, "git", "-c", "core.hooksPath=/dev/null", "clone", "--no-hardlinks", "--no-checkout", r.ProjectRoot(), path)
				process.Dir = t.TempDir()
				if _, err := process.CombinedOutput(); err != nil {
					t.Fatal(err)
				}
				checkout := exec.CommandContext(ctx, "git", "-c", "core.hooksPath=/dev/null", "-C", path, "checkout", "--detach", head)
				if _, err := checkout.CombinedOutput(); err != nil {
					t.Fatal(err)
				}
			}
			if _, _, _, err := r.resolveRequiredCompletionSource(ctx, root); err == nil {
				t.Fatal("caller replaced approved source identity without new owned observation")
			}
		})
	}
}
