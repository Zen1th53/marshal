package tmux

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/statepath"
)

func listenSocket(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
}

// A control centre started under another temporary directory lives on a
// tmux server MARSHAL's own socket does not reach (finding 73). Its socket
// is listed; MARSHAL's own socket and plain files are not.
func TestOtherServerSocketsFindsOtherTemporaryDirectories(t *testing.T) {
	base, err := os.MkdirTemp("", "mxs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(base) })
	uid := os.Getuid()
	oldTmp := filepath.Join(base, "o")
	ownTmux := filepath.Join(base, "n")
	t.Setenv("TMPDIR", oldTmp)
	t.Setenv("TMUX_TMPDIR", ownTmux)
	older := filepath.Join(oldTmp, fmt.Sprintf(statepath.Name+"-tmux-%d", uid), fmt.Sprintf("tmux-%d", uid), "default")
	plain := filepath.Join(oldTmp, fmt.Sprintf("tmux-%d", uid), "work")
	listenSocket(t, older)
	listenSocket(t, plain)
	listenSocket(t, OwnSocketPath())
	if err := os.WriteFile(filepath.Join(filepath.Dir(plain), "notes"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	got := OtherServerSockets()
	if !slices.Contains(got, older) || !slices.Contains(got, plain) {
		t.Fatalf("other sockets = %v, want %s and %s", got, older, plain)
	}
	for _, s := range got {
		if sameSocket(s, OwnSocketPath()) || strings.HasSuffix(s, "notes") {
			t.Fatalf("listed %s", s)
		}
	}
}

// A server counts only when it names the session exactly.
func TestSessionElsewhereNeedsTheServerToNameTheSession(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "tmux")
	script := "#!/bin/sh\nif [ \"$1\" = -S ] && [ \"$2\" = /old ] && [ \"$3\" = list-sessions ]; then printf 'marshal-p-1x\\nmarshal-p-1\\n'; fi\nexit 0\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	SetBinaryPath(fake)
	defer ResetBinaryPath()
	ctx := context.Background()
	if got := SessionElsewhere(ctx, "marshal-p-1", []string{"/silent", "/old"}); got != "/old" {
		t.Fatalf("found on %q, want /old", got)
	}
	if got := SessionElsewhere(ctx, "marshal-p", []string{"/silent", "/old"}); got != "" {
		t.Fatalf("a prefix matched on %q", got)
	}
}
