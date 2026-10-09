package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
)

func TestControlCenterOpensWithLiveRuntimeOwner(t *testing.T) {
	t.Setenv("MARSHAL_NO_UPDATE_CHECK", "1")
	bin := t.TempDir()
	for _, name := range []string{"codex", "claude", "opencode", "agy", "tmux"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\necho 'tmux 3.4'\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1")
	t.Setenv("TMUX", "/tmp/owner-test,1,0")
	repo := cliRepo(t)
	if _, err := app.Bootstrap(t.Context(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	rt, err := app.Open(t.Context(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	var stdout, errout bytes.Buffer
	code := Execute(t.Context(), repo.Path(), []string{"tui"}, strings.NewReader("/quit\n"), &stdout, &errout)
	out, stderr := stdout.String(), errout.String()
	if code != 0 || !strings.Contains(out, "Exiting MARSHAL terminal workspace") || strings.Contains(out+stderr, "owns this project") || strings.Contains(out+stderr, "already running") {
		t.Fatalf("unexpected owner refusal: code=%d stdout=%q stderr=%q", code, out, stderr)
	}
}
