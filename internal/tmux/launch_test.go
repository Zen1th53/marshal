package tmux

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLargeWindowCommandPreservesLiteralArguments(t *testing.T) {
	for _, respawn := range []bool{false, true} {
		name := "new"
		if respawn {
			name = "respawn"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("TMPDIR", dir)
			fake := filepath.Join(dir, "tmux")
			// Model tmux's bounded command message, then run the pane command.
			script := `#!/bin/sh
size=0
for arg do size=$((size + ${#arg} + 1)); done
if [ "$size" -ge 16384 ]; then echo 'command too long' >&2; exit 1; fi
shift
while [ "$#" -gt 0 ]; do
  case "$1" in
    -t|-n|-c) shift 2 ;;
    -k|-d) shift ;;
    *) break ;;
  esac
done
exec "$@"
`
			if err := os.WriteFile(fake, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			SetBinaryPath(fake)
			defer ResetBinaryPath()
			output := filepath.Join(dir, "output")
			injected := filepath.Join(dir, "injected")
			prompt := strings.Repeat("MARSHAL PROTOCOL\n", 1600) + "'\"; touch " + injected + "\n$(false) `false` \\ end"
			command := []string{"/bin/sh", "-c", `printf '%s' "$1" > "$2"`, "fixture", prompt, output}
			var err error
			if respawn {
				err = RespawnWindow(context.Background(), "%1", command)
			} else {
				err = NewWindow(context.Background(), "test", "chat", dir, nil, command)
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(output)
			if err != nil || string(got) != prompt {
				t.Fatalf("prompt changed: bytes=%d, err=%v", len(got), err)
			}
			if _, err := os.Stat(injected); !os.IsNotExist(err) {
				t.Fatalf("prompt executed as shell code: %v", err)
			}
			leftovers, err := filepath.Glob(filepath.Join(dir, "marshal-tmux-launch-*"))
			if err != nil || len(leftovers) != 0 {
				t.Fatalf("launch files were not removed: %v, %v", leftovers, err)
			}
		})
	}
}

func TestLargeWindowCommandCleansUpAfterLaunchFailure(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	fake := filepath.Join(dir, "tmux")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	SetBinaryPath(fake)
	defer ResetBinaryPath()
	if err := NewWindow(context.Background(), "test", "chat", dir, nil, []string{"codex", strings.Repeat("prompt", 4000)}); err == nil {
		t.Fatal("launch failure was lost")
	}
	leftovers, err := filepath.Glob(filepath.Join(dir, "marshal-tmux-launch-*"))
	if err != nil || len(leftovers) != 0 {
		t.Fatalf("failed launch left files: %v, %v", leftovers, err)
	}
}
