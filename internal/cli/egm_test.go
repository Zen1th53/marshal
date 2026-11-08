package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/egm"
)

func TestEGMStatusAndInstallWorkWithoutRuntime(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MARSHAL_EGM_CODEX_ROOT", filepath.Join(root, "missing"))
	for _, args := range [][]string{{"egm", "status", "--provider", "codex"}, {"egm", "install", "--provider", "codex"}, {"egm", "install", "--provider", "claude"}, {"egm", "install", "--provider", "antigravity"}} {
		var out, stderr bytes.Buffer
		code := Execute(context.Background(), root, append([]string{"--json"}, args...), strings.NewReader(""), &out, &stderr)
		if code != 0 {
			t.Fatalf("%v: %d %s", args, code, stderr.String())
		}
		if args[1] == "status" {
			var statuses []egm.Status
			if err := json.Unmarshal(out.Bytes(), &statuses); err != nil || len(statuses) != 1 || !statuses[0].Fallback || statuses[0].State != egm.Missing {
				t.Fatalf("status: %s %v", out.String(), err)
			}
		} else if !strings.Contains(out.String(), `"executed": false`) || !strings.Contains(out.String(), egm.Lock().Commit) {
			t.Fatalf("unpinned install: %s", out.String())
		}
	}
	for _, args := range [][]string{{"egm"}, {"egm", "wat"}, {"egm", "status", "--provider", "bogus"}, {"egm", "install", "--provider", "ollama"}, {"egm", "record", "--file", "x"}} {
		var out, stderr bytes.Buffer
		if code := Execute(context.Background(), root, args, strings.NewReader(""), &out, &stderr); code == 0 {
			t.Fatalf("accepted invalid args: %v", args)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".marshal")); !os.IsNotExist(err) {
		t.Fatalf("status/install created runtime: %v", err)
	}
}

func TestEGMRecordRejectsFilesOutsideProject(t *testing.T) {
	ctx := context.Background()
	repo := cliRepo(t)
	if _, err := app.Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(repo.Path(), "linked.md")); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{outside, "../outside.md", "linked.md", "."} {
		var out, stderr bytes.Buffer
		code := Execute(ctx, repo.Path(), []string{"egm", "record", "--stage", "concepts_brief", "--provider", "codex", "--egm-version", "builtin", "--file", file}, strings.NewReader(""), &out, &stderr)
		if code == 0 {
			t.Fatalf("read outside project/non-file: %s", file)
		}
	}
}
