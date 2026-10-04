package worker

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHoneypotIsolation(t *testing.T) {
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-qm", "initial"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("%v %s", err, out)
		}
	}
	a, err := NewHoneypot(repo)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := NewHoneypot(repo)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	for i, token := range a.tokens {
		if token == b.tokens[i] {
			t.Fatal("tokens reused")
		}
		if !a.Contains([]byte(token)) {
			t.Fatal("token not detected")
		}
	}
	if a.Contains([]byte("clean hand-in using GITHUB_TOKEN by name")) {
		t.Fatal("clean text rejected")
	}
	for _, name := range []string{".aws/credentials", ".config/gh/hosts.yml", ".env"} {
		data, err := os.ReadFile(filepath.Join(a.Home, name))
		if err != nil || !a.Contains(data) {
			t.Fatalf("decoy %s: %v", name, err)
		}
	}
	if strings.HasPrefix(a.Home, a.worktree+string(os.PathSeparator)) {
		t.Fatal("home inside repository")
	}
}

func TestHoneypotRefusesHandIn(t *testing.T) {
	for _, surface := range []string{"output", "file", "binary", "commit", "history", "file_path"} {
		t.Run(surface, func(t *testing.T) {
			repo := t.TempDir()
			git := func(args ...string) {
				t.Helper()
				cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("git: %v %s", err, out)
				}
			}
			git("init", "-q")
			git("config", "user.name", "Test")
			git("config", "user.email", "test@example.invalid")
			git("commit", "--allow-empty", "-qm", "initial")
			trap, err := NewHoneypot(repo)
			if err != nil {
				t.Fatal(err)
			}
			defer trap.Close()
			if err := trap.Check(context.Background(), nil, nil); err != nil {
				t.Fatal(err)
			}
			var output []byte
			switch surface {
			case "output":
				output = []byte(trap.tokens[0])
			case "file_path":
				if err := os.WriteFile(filepath.Join(repo, trap.tokens[0]), nil, 0600); err != nil {
					t.Fatal(err)
				}
			case "file", "binary":
				data := []byte(trap.tokens[0])
				if surface == "binary" {
					data = append([]byte{0}, data...)
				}
				if err := os.WriteFile(filepath.Join(repo, "result"), data, 0600); err != nil {
					t.Fatal(err)
				}
			case "history":
				if err := os.WriteFile(filepath.Join(repo, "result"), append([]byte{0}, []byte(trap.tokens[0])...), 0600); err != nil {
					t.Fatal(err)
				}
				git("add", "result")
				git("commit", "-qm", "write result")
				git("rm", "result")
				git("commit", "-qm", "remove result")
			case "commit":
				git("commit", "--allow-empty", "-qm", trap.tokens[0])
				git("commit", "--allow-empty", "-qm", "clean final")
			}
			if err := trap.Check(context.Background(), output, nil); err == nil {
				t.Fatal("contaminated hand-in accepted")
			}
			if _, err := os.Stat(filepath.Join(trap.Home, "../evidence.json")); err != nil {
				t.Fatal("missing evidence", err)
			}
			if surface == "file_path" {
				data, err := os.ReadFile(filepath.Join(trap.root, "evidence.json"))
				if err != nil {
					t.Fatal(err)
				}
				var evidence map[string]json.RawMessage
				if err := json.Unmarshal(data, &evidence); err != nil {
					t.Fatal(err)
				}
				var name []byte
				if err := json.Unmarshal(evidence["file_path:"+trap.tokens[0]], &name); err != nil || string(name) != trap.tokens[0] {
					t.Fatal("missing file path evidence")
				}
				if err := os.Remove(filepath.Join(repo, trap.tokens[0])); err != nil {
					t.Fatal(err)
				}
				if err := trap.Check(context.Background(), nil, nil); !errors.Is(err, ErrHoneypot) {
					t.Fatal("path hit cleared by clean retry")
				}
			}
		})
	}
}

func TestHoneypotScansSplitAndTruncatedStream(t *testing.T) {
	trap, err := NewHoneypot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer trap.Close()
	token := trap.tokens[0]
	capture := &limitedBuffer{limit: 4, observe: func(data []byte) { trap.Observe("stdout", data) }}
	capture.Write([]byte(strings.Repeat("clean", 100)))
	capture.Write([]byte(token[:10]))
	capture.Write([]byte(token[10:]))
	if !capture.Truncated() || trap.Contains(capture.Bytes()) {
		t.Fatal("test did not hide token from captured output")
	}
	if err := trap.Check(t.Context(), capture.Bytes(), nil); err == nil {
		t.Fatal("stream hit missed")
	}
	if err := trap.Check(t.Context(), nil, nil); err == nil {
		t.Fatal("hit cleared by clean retry")
	}
}

func TestHoneypotLeavesRepositoryClean(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "project.txt"), []byte("project\n"), 0600); err != nil {
		t.Fatal(err)
	}
	trap, err := NewHoneypot(repo)
	if err != nil {
		t.Fatal(err)
	}
	defer trap.Close()
	if err := filepath.WalkDir(repo, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if trap.Contains(data) {
			t.Fatal("seeded token in repository")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
