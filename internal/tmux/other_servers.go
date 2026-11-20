package tmux

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/statepath"
)

// OtherServerSockets are the sockets of tmux servers other than MARSHAL's
// own that a control centre for a project may still run on: MARSHAL's own
// socket directory under every temporary directory a launch may have had
// (TMPDIR differs between a login shell and an ssh or VM shell, and an
// older build or a TMUX_TMPDIR the owner set put it elsewhere), and tmux's
// own default socket directory. Only live sockets are listed, each once,
// and MARSHAL's current own socket never.
func OtherServerSockets() []string {
	uid := os.Getuid()
	own := OwnSocketPath()
	var bases []string
	for _, b := range []string{os.Getenv("TMUX_TMPDIR"), os.TempDir(), os.Getenv("TMPDIR"), "/tmp", "/var/tmp"} {
		if b != "" {
			bases = append(bases, b)
		}
	}
	var dirs []string
	for _, b := range bases {
		dirs = append(dirs,
			filepath.Join(b, fmt.Sprintf(statepath.Name+"-tmux-%d", uid), fmt.Sprintf("tmux-%d", uid)),
			filepath.Join(b, fmt.Sprintf("tmux-%d", uid)))
	}
	var out []string
	seen := map[string]bool{}
	for _, d := range dirs {
		entries, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range entries {
			path := filepath.Join(d, e.Name())
			info, err := os.Lstat(path)
			if err != nil || info.Mode()&os.ModeSocket == 0 {
				continue
			}
			key := path
			if r, err := filepath.EvalSymlinks(d); err == nil {
				key = filepath.Join(r, e.Name())
			}
			if seen[key] || sameSocket(path, own) {
				continue
			}
			seen[key] = true
			out = append(out, path)
		}
	}
	return out
}

// socketCommand is tmux run against the server at socket.
func socketCommand(ctx context.Context, socket string, args ...string) (*exec.Cmd, error) {
	if err := requireTarget(socket); err != nil {
		return nil, err
	}
	bin, err := FindBinary()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, bin, append([]string{"-S", socket}, args...)...)
	cmd.WaitDelay = 100 * time.Millisecond
	return cmd, nil
}

// SessionOnServer reports whether the tmux server at socket runs exactly
// session: the server must name it, an exit status alone is not taken as
// an answer.
func SessionOnServer(ctx context.Context, socket, session string) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if requireTarget(session) != nil {
		return false
	}
	cmd, err := socketCommand(ctx, socket, "list-sessions", "-F", "#{session_name}")
	if err != nil {
		return false
	}
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == session {
			return true
		}
	}
	return false
}

// SessionElsewhere is the socket, among sockets, of a server that runs
// session; "" when none does.
func SessionElsewhere(ctx context.Context, session string, sockets []string) string {
	for _, s := range sockets {
		if SessionOnServer(ctx, s, session) {
			return s
		}
	}
	return ""
}

// KillSessionOnServer ends session on the tmux server at socket, and every
// process its windows run.
func KillSessionOnServer(ctx context.Context, socket, session string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := requireTarget(session); err != nil {
		return err
	}
	cmd, err := socketCommand(ctx, socket, "kill-session", "-t", "="+session)
	if err != nil {
		return err
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux kill-session: %s: %w", string(out), err)
	}
	return nil
}

// AttachSessionOnServer attaches the terminal to session on the tmux server
// at socket.
func AttachSessionOnServer(ctx context.Context, socket, session string, stdin io.Reader, stdout, stderr io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, 24*time.Hour)
	defer cancel()
	if err := requireTarget(session); err != nil {
		return err
	}
	cmd, err := socketCommand(ctx, socket, "attach-session", "-t", "="+session)
	if err != nil {
		return err
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	return cmd.Run()
}
