package optimization

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/learning"
)

func TestBwrapReplayRunnerUsesOfflineSandboxAndIndependentVerifier(t *testing.T) {
	root := t.TempDir()
	tree := filepath.Join(root, "tree")
	if err := os.Mkdir(tree, 0o700); err != nil {
		t.Fatal(err)
	}
	argsFile := filepath.Join(root, "args")
	fake := filepath.Join(root, "bwrap")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + argsFile + "'\nexit 0\n"
	if err := os.WriteFile(fake, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	runner := BwrapReplayRunner{
		BwrapBinary: fake,
		ResolveTree: func(context.Context, string) (string, error) { return tree, nil },
		CommandFor:  func(ReplayRequest) ([]string, error) { return []string{"/bin/true"}, nil },
		Verify: func(_ context.Context, _ ReplayRequest, output []byte, runErr error) (ReplayObservation, error) {
			if runErr != nil {
				t.Fatalf("sandbox run: %v %s", runErr, output)
			}
			return ReplayObservation{Outcome: learning.OutcomeVerifiedComplete, VerifierResult: StatusPass}, nil
		},
	}
	_, err := ExecuteReplay(context.Background(), runner, cfFactual(learning.OutcomeFailed, StatusFail), cfRoute("claude"), SandboxPolicy{WritableRoot: root, MaxWallMillis: 1000, MaxMemoryBytes: 1 << 20}, "test", "cluster", cfGovernance(), cfNow())
	if err != nil {
		t.Fatal(err)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "--unshare-net") {
		t.Fatalf("replay did not deny network: %s", args)
	}
}

func TestBwrapReplayRunnerRefusesTreeOutsideBound(t *testing.T) {
	runner := BwrapReplayRunner{ResolveTree: func(context.Context, string) (string, error) { return t.TempDir(), nil }, CommandFor: func(ReplayRequest) ([]string, error) { return []string{"/bin/true"}, nil }, Verify: func(context.Context, ReplayRequest, []byte, error) (ReplayObservation, error) {
		return ReplayObservation{}, nil
	}}
	_, err := runner.Replay(context.Background(), ReplayRequest{TreeDigest: "tree", Sandbox: SandboxPolicy{WritableRoot: t.TempDir()}})
	if err == nil || !strings.Contains(err.Error(), "outside bounded writable root") {
		t.Fatalf("outside tree error=%v", err)
	}
}
