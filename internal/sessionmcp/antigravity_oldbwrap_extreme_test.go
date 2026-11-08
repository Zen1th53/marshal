//go:build !governed && linux

package sessionmcp_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/sandbox"
)

// bwrapListsSize reports whether the installed bubblewrap understands --size.
func bwrapListsSize(t *testing.T) bool {
	t.Helper()
	out, _ := exec.CommandContext(t.Context(), "/usr/bin/bwrap", "--help").CombinedOutput()
	return strings.Contains(string(out), "--size")
}

// oldBwrap installs a bubblewrap that behaves like the 0.6 series shipped with
// Ubuntu 22.04: --size is neither documented nor accepted.
func oldBwrap(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bwrap")
	script := `#!/bin/sh
if [ "$1" = "--help" ]; then
  /usr/bin/bwrap --help 2>&1 | grep -v -e '--size'
  exit 0
fi
for a in "$@"; do
  if [ "$a" = "--size" ]; then echo "bwrap: Unknown option --size" >&2; exit 1; fi
done
exec /usr/bin/bwrap "$@"
`
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAntigravityProjectCacheFallsBackWithoutBwrapSize(t *testing.T) {
	req := agyProjectsRequest(t)
	const command = `set -eu
test "$(cat /home/marshal/.gemini/config/mcp_config.json)" = '{"mcpServers":{}}'
printf ordinary > ordinary.txt
mkdir /home/marshal/.gemini/config/projects/synthetic
printf x > /home/marshal/.gemini/config/projects/synthetic/project.json
if printf foreign > /home/marshal/.gemini/config/mcp_config.json; then exit 21; fi
printf PROJECT_CACHE_OK`
	spec, err := sandbox.NewBwrap(oldBwrap(t)).Wrap(req, []string{"/bin/sh", "-c", command})
	if err != nil {
		t.Fatal(err)
	}
	for _, arg := range spec.Args {
		if arg == "--size" {
			t.Fatal("--size passed to a bubblewrap that does not support it")
		}
	}
	if !strings.Contains(spec.Isolation.Reason, "bubblewrap lacks --size") {
		t.Fatalf("fallback not recorded as evidence: %q", spec.Isolation.Reason)
	}
	if out, err := exec.CommandContext(t.Context(), spec.Path, spec.Args...).CombinedOutput(); err != nil {
		t.Fatalf("project cache unavailable on old bubblewrap: %v %s", err, out)
	}
	if body, err := os.ReadFile(filepath.Join(req.Worktree, "ordinary.txt")); err != nil || string(body) != "ordinary" {
		t.Fatal("ordinary workspace writes unavailable")
	}
}

func TestAntigravityProjectCacheKeepsSizeBoundWhenSupported(t *testing.T) {
	if !bwrapListsSize(t) {
		t.Skip("installed bubblewrap lacks --size; the 16 MiB bound cannot be applied")
	}
	spec, err := sandbox.NewBwrap("/usr/bin/bwrap").Wrap(agyProjectsRequest(t), []string{"/bin/true"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(spec.Isolation.Reason, "lacks --size") {
		t.Fatalf("bound dropped on a capable bubblewrap: %q", spec.Isolation.Reason)
	}
	if !strings.Contains(strings.Join(spec.Args, " "), "--size 16777216 --tmpfs ") {
		t.Fatal("capable bubblewrap did not receive the 16 MiB project cache bound")
	}
}
