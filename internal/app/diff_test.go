package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func diffTestGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return string(out)
}
func diffTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	diffTestGit(t, dir, "init", "-q")
	diffTestWrite(t, dir, "tracked", "base\n")
	diffTestGit(t, dir, "add", "--", "tracked")
	diffTestGit(t, dir, "commit", "-qm", "base")
	return dir
}
func diffTestWrite(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}
func TestDiffInventoryScopes(t *testing.T) {
	for _, kind := range []string{"staged", "untracked", "mixed"} {
		t.Run(kind, func(t *testing.T) {
			dir := diffTestRepo(t)
			if kind != "untracked" {
				diffTestWrite(t, dir, "tracked", "staged\n")
				diffTestGit(t, dir, "add", "--", "tracked")
			}
			if kind == "mixed" {
				diffTestWrite(t, dir, "tracked", "unstaged\n")
			}
			if kind != "staged" {
				diffTestWrite(t, dir, "new", "untracked\n")
			}
			inv, err := LoadDiffInventory(context.Background(), dir, "")
			if err != nil {
				t.Fatal(err)
			}
			want := 1
			if kind == "mixed" {
				want = 3
			}
			if len(inv.Entries) != want {
				t.Fatalf("%+v", inv)
			}
			for _, scope := range []string{"staged", "unstaged", "untracked"} {
				only, e := LoadDiffInventory(context.Background(), dir, scope)
				if e != nil {
					t.Fatal(e)
				}
				wantCount := 0
				for _, entry := range inv.Entries {
					if entry.Scope == scope {
						wantCount++
					}
				}
				if len(only.Entries) != wantCount {
					t.Fatalf("%s: got %d entries, want %d", scope, len(only.Entries), wantCount)
				}
				for _, entry := range only.Entries {
					if entry.Scope != scope {
						t.Fatal(entry)
					}
				}
			}
			if strings.Contains(diffTestGit(t, dir, "ls-files", "--stage"), "new") {
				t.Fatal("preview staged untracked file")
			}
		})
	}
}
func TestDiffInventoryUnusualNames(t *testing.T) {
	dir := diffTestRepo(t)
	names := []string{"space name", "世界", "-dash", "glob[1]", "line\nbreak"}
	for _, name := range names {
		diffTestWrite(t, dir, name, "initial\n")
	}
	diffTestGit(t, dir, "add", "--", ".")
	diffTestGit(t, dir, "commit", "-qm", "names")
	for _, name := range names {
		diffTestWrite(t, dir, name, "changed\n")
	}
	inv, e := LoadDiffInventory(context.Background(), dir, "unstaged")
	if e != nil {
		t.Fatal(e)
	}
	if len(inv.Entries) != len(names) {
		t.Fatal(inv)
	}
	for _, entry := range inv.Entries {
		if !strings.Contains(entry.Preview, "+changed") {
			t.Fatal(entry)
		}
	}
}
func TestDiffInventorySafetyAndBounds(t *testing.T) {
	dir := diffTestRepo(t)
	diffTestWrite(t, dir, "binary", "a\x00secretbytes")
	diffTestWrite(t, dir, "large", strings.Repeat("line\n", DiffMaxFileBytes))
	diffTestWrite(t, dir, "secret", "password=hunter2\napi_key=abc\n")
	outside := filepath.Join(t.TempDir(), "outside")
	if e := os.WriteFile(outside, []byte("outside-secret"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(outside, filepath.Join(dir, "link")); e != nil {
		t.Fatal(e)
	}
	inv, e := LoadDiffInventory(context.Background(), dir, "untracked")
	if e != nil {
		t.Fatal(e)
	}
	previews := fmt.Sprint(inv)
	for _, bad := range []string{"hunter2", "outside-secret", "secretbytes"} {
		if strings.Contains(previews, bad) {
			t.Fatal("secret/binary leaked")
		}
	}
	for _, marker := range []string{"binary file", "preview truncated", "symlink", "[REDACTED]"} {
		if !strings.Contains(previews, marker) {
			t.Fatalf("missing %s", marker)
		}
	}
	for i := 0; i < DiffMaxFiles+10; i++ {
		diffTestWrite(t, dir, fmt.Sprintf("count-%03d", i), "x")
	}
	inv, e = LoadDiffInventory(context.Background(), dir, "")
	if e != nil {
		t.Fatal(e)
	}
	if len(inv.Entries) > DiffMaxFiles || !inv.Truncated {
		t.Fatal(inv)
	}
}
func TestDiffInventoryGitFailure(t *testing.T) {
	if inv, e := LoadDiffInventory(context.Background(), t.TempDir(), ""); e == nil || len(inv.Entries) != 0 {
		t.Fatalf("%+v %v", inv, e)
	}
	dir := diffTestRepo(t)
	fake := t.TempDir()
	if e := os.WriteFile(filepath.Join(fake, "git"), []byte("#!/bin/sh\nexit 42\n"), 0700); e != nil {
		t.Fatal(e)
	}
	t.Setenv("PATH", fake)
	if _, e := LoadDiffInventory(context.Background(), dir, ""); e == nil {
		t.Fatal("false clean inventory")
	}
}
func TestDiffInventoryDisablesGitHelpers(t *testing.T) {
	dir := diffTestRepo(t)
	marker := filepath.Join(dir, "executed")
	helper := filepath.Join(dir, "helper")
	diffTestWrite(t, dir, "helper", "#!/bin/sh\necho ran > "+marker+"\n")
	if e := os.Chmod(helper, 0700); e != nil {
		t.Fatal(e)
	}
	diffTestWrite(t, dir, ".gitattributes", "tracked diff=hostile\n")
	diffTestGit(t, dir, "config", "diff.hostile.textconv", helper)
	diffTestGit(t, dir, "config", "diff.external", helper)
	diffTestWrite(t, dir, "tracked", "changed\n")
	if _, e := LoadDiffInventory(context.Background(), dir, ""); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(marker); !os.IsNotExist(e) {
		t.Fatal("Git executed configured helper")
	}
}

func TestDiffInventoryTotalByteBound(t *testing.T) {
	dir := diffTestRepo(t)
	for i := 0; i < 30; i++ {
		diffTestWrite(t, dir, fmt.Sprintf("large-%02d", i), strings.Repeat("x", DiffMaxFileBytes-1)+"\n")
	}
	inv, err := LoadDiffInventory(context.Background(), dir, "untracked")
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, entry := range inv.Entries {
		if len(entry.Preview) > DiffMaxFileBytes {
			t.Fatalf("per-file bound: %d", len(entry.Preview))
		}
		total += len(entry.Preview)
	}
	if total > DiffMaxTotalBytes || !inv.Truncated {
		t.Fatalf("total=%d truncated=%v", total, inv.Truncated)
	}
}

func TestDiffInventoryTrackedBinaryAndRedaction(t *testing.T) {
	dir := diffTestRepo(t)
	diffTestWrite(t, dir, "tracked", "a\x00binary-private\n")
	inv, err := LoadDiffInventory(context.Background(), dir, "unstaged")
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Entries) != 1 || !strings.Contains(inv.Entries[0].Preview, "Binary files") || strings.Contains(inv.Entries[0].Preview, "binary-private") {
		t.Fatal(inv)
	}
	diffTestWrite(t, dir, "tracked", "password=hunter2\n")
	diffTestGit(t, dir, "add", "--", "tracked")
	inv, err = LoadDiffInventory(context.Background(), dir, "staged")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fmt.Sprint(inv), "hunter2") || !strings.Contains(fmt.Sprint(inv), "[REDACTED]") {
		t.Fatal(inv)
	}
}

func TestDiffInventoryForcedTextBinary(t *testing.T) {
	dir := diffTestRepo(t)
	diffTestWrite(t, dir, ".gitattributes", "tracked diff\n")
	diffTestWrite(t, dir, "tracked", "a\x00binary-private\n")
	inv, err := LoadDiffInventory(context.Background(), dir, "unstaged")
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Entries) != 1 || !strings.Contains(inv.Entries[0].Preview, "binary file") || strings.Contains(inv.Entries[0].Preview, "binary-private") {
		t.Fatal(inv)
	}
}
