package sandbox

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

// Seatbelt enforces host-path policy without a process or mount namespace.
type Seatbelt struct{ binary string }

// Production selection always supplies /usr/bin/sandbox-exec. An explicit path
// permits testing absence without consulting PATH.
func NewSeatbelt(binary string) *Seatbelt { return &Seatbelt{binary: binary} }

func (s *Seatbelt) unavailable(reason string) model.IsolationCapability {
	return model.IsolationCapability{Level: model.IsolationBlocked, Reason: "sandboxed execution unavailable: seatbelt: " + reason + "; governed execution is blocked"}
}

func (s *Seatbelt) checkBinary() error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("requires macOS")
	}
	if !filepath.IsAbs(s.binary) {
		return fmt.Errorf("sandbox-exec path must be absolute")
	}
	info, err := os.Stat(s.binary)
	if err != nil {
		return fmt.Errorf("sandbox-exec unavailable: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 || info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("sandbox-exec is not a trusted executable")
	}
	return nil
}

func (s *Seatbelt) Probe(ctx context.Context) model.IsolationCapability {
	return s.ProbeRequest(ctx, model.SandboxRequest{})
}

// ProbeRequest verifies the backend's deny-default policy. Network describes
// the requested mode, not endpoint filtering; process namespaces never exist.
func (s *Seatbelt) ProbeRequest(ctx context.Context, request model.SandboxRequest) model.IsolationCapability {
	if err := s.checkBinary(); err != nil {
		return s.unavailable(err.Error())
	}
	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	root, err := os.MkdirTemp("/tmp", "marshal-seatbelt-probe-")
	if err != nil {
		return s.unavailable("create probe: " + err.Error())
	}
	defer os.RemoveAll(root)
	worktree := filepath.Join(root, "worktree")
	outside := filepath.Join(root, "outside")
	for _, path := range []string{worktree, outside} {
		if err := os.Mkdir(path, 0o700); err != nil {
			return s.unavailable(err.Error())
		}
	}
	provider := filepath.Join(worktree, ".codex")
	if err := os.Mkdir(provider, 0o700); err != nil {
		return s.unavailable(err.Error())
	}
	providerSentinel := filepath.Join(provider, "sentinel")
	if err := os.WriteFile(providerSentinel, []byte("provider-private"), 0o600); err != nil {
		return s.unavailable(err.Error())
	}
	sentinel := filepath.Join(outside, "sentinel")
	if err := os.WriteFile(sentinel, []byte("denial-proof"), 0o600); err != nil {
		return s.unavailable(err.Error())
	}
	if err := os.Symlink(outside, filepath.Join(worktree, "escape")); err != nil {
		return s.unavailable(err.Error())
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return s.unavailable("loopback probe: " + err.Error())
	}
	defer listener.Close()
	socket := filepath.Join(root, "socket")
	unixListener, err := net.Listen("unix", socket)
	if err != nil {
		return s.unavailable("Unix socket probe: " + err.Error())
	}
	defer unixListener.Close()
	port := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	// nc -z does not work with Unix sockets on macOS, so those checks connect
	// with an empty input instead.
	// Successful allowed connections establish that nc and both listeners work;
	// a missing helper or a broken listener cannot masquerade as denial.
	allowed, err := s.envelope(model.SandboxRequest{Worktree: root, NetworkAllowed: true}, []string{"/bin/sh", "-c", `/usr/bin/nc -z -w 1 127.0.0.1 "$1" && /usr/bin/nc -w 1 -U "$2" </dev/null`, "probe", port, socket})
	if err != nil {
		return s.unavailable(err.Error())
	}
	output, runErr := runSeatbelt(probeCtx, allowed)
	allowed.Cleanup()
	if runErr != nil {
		return s.unavailable(fmt.Sprintf("profile or network control probe failed: %v: %s", runErr, bounded(output, 256)))
	}
	script := `set -eu
printf proof > "$PWD/proof"
/bin/cat "$PWD/proof" > "$TMPDIR/proof"
if /bin/cat "$1"; then exit 21; fi
if (printf escape > "$2/write"); then exit 22; fi
if /bin/cat "$PWD/escape/sentinel"; then exit 23; fi
if /bin/cat "$PWD/.codex/sentinel"; then exit 28; fi
if /bin/sh -c '/bin/cat "$1"' child "$1"; then exit 24; fi
if /bin/kill -CONT "$3"; then exit 25; fi
if /usr/bin/nc -z -w 1 127.0.0.1 "$4"; then exit 26; fi
if /usr/bin/nc -w 1 -U "$5" </dev/null; then exit 27; fi
printf seatbelt-denial-verified
`
	denied, err := s.envelope(model.SandboxRequest{Worktree: worktree}, []string{"/bin/sh", "-c", script, "probe", sentinel, outside, strconv.Itoa(os.Getpid()), port, socket})
	if err != nil {
		return s.unavailable(err.Error())
	}
	output, runErr = runSeatbelt(probeCtx, denied)
	denied.Cleanup()
	if runErr != nil || !strings.Contains(string(output), "seatbelt-denial-verified") {
		return s.unavailable(fmt.Sprintf("live denial probe failed: %v: %s", runErr, bounded(output, 256)))
	}
	return model.IsolationCapability{Level: model.IsolationSeatbelt, Available: true, Filesystem: true, Process: false, Network: request.NetworkAllowed, Reason: "seatbelt live denial probe passed; no process namespace; per-endpoint egress is not enforced"}
}

func runSeatbelt(ctx context.Context, spec model.CommandSpec) ([]byte, error) {
	cmd := exec.CommandContext(ctx, spec.Path, spec.Args...)
	cmd.Dir, cmd.Env = spec.Dir, spec.Env
	return cmd.CombinedOutput()
}

func (s *Seatbelt) Wrap(request model.SandboxRequest, command []string) (model.CommandSpec, error) {
	capability := s.ProbeRequest(context.Background(), request)
	if !capability.Available {
		return model.CommandSpec{}, fmt.Errorf("%w: %s", model.ErrUnavailable, capability.Reason)
	}
	spec, err := s.envelope(request, command)
	if err != nil {
		return model.CommandSpec{}, err
	}
	// Reject the actual parameterized policy before returning an executable
	// envelope; the caller's command is never used as the profile probe.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	check := spec
	check.Args = append(append([]string(nil), spec.Args[:len(spec.Args)-len(command)]...), "/usr/bin/true")
	if output, err := runSeatbelt(ctx, check); err != nil {
		spec.Cleanup()
		return model.CommandSpec{}, fmt.Errorf("%w: seatbelt request profile rejected: %v: %s", model.ErrUnavailable, err, bounded(output, 256))
	}
	spec.Isolation = capability
	return spec, nil
}

func canonicalSeatbeltPath(path string, directory bool) (string, error) {
	if err := validSeatbeltPath(path); err != nil {
		return "", err
	}
	resolved, err := existingPath(path)
	if err != nil {
		return "", err
	}
	if err := validSeatbeltPath(resolved); err != nil {
		return "", err
	}
	if directory {
		info, err := os.Stat(resolved)
		if err != nil {
			return "", err
		}
		if !info.IsDir() {
			return "", fmt.Errorf("not a directory: %q", path)
		}
	}
	return resolved, nil
}

func normalizeSeatbeltRequest(request model.SandboxRequest, scratch string) (seatbeltProfileRequest, error) {
	var err error
	request.Worktree, err = canonicalSeatbeltPath(request.Worktree, true)
	if err != nil {
		return seatbeltProfileRequest{}, err
	}
	if request.RuntimeDir != "" {
		request.RuntimeDir, err = canonicalSeatbeltPath(request.RuntimeDir, true)
		if err != nil {
			return seatbeltProfileRequest{}, err
		}
	}
	if request.RuntimeDir != "" && request.RuntimeDir == request.Worktree {
		return seatbeltProfileRequest{}, fmt.Errorf("worktree cannot be the runtime directory")
	}
	input := seatbeltProfileRequest{Request: request, Scratch: scratch}
	input.Request.WritableDirs = nil
	home, err := os.UserHomeDir()
	if err != nil {
		return input, err
	}
	home, err = canonicalSeatbeltPath(home, true)
	if err != nil {
		return input, err
	}
	safeScope := func(path string) error {
		if path == "/" || path == home || pathWithin(path, home) || isSeatbeltProviderState(path) || isForbiddenCredentialPath(path) {
			return fmt.Errorf("protected or overly broad scope: %q", path)
		}
		if seatbeltRuntimeStateScope(request, path) {
			return fmt.Errorf("runtime state scope forbidden: %q", path)
		}
		return nil
	}
	if err := safeScope(request.Worktree); err != nil {
		return input, err
	}
	for _, path := range request.WritableDirs {
		resolved, err := canonicalSeatbeltPath(path, true)
		if err != nil {
			return input, err
		}
		if err := safeScope(resolved); err != nil {
			return input, err
		}
		input.Request.WritableDirs = append(input.Request.WritableDirs, resolved)
	}
	for _, bind := range request.ReadOnlyBinds {
		if err := validSeatbeltPath(bind.Target); err != nil {
			return input, err
		}
		source, err := canonicalSeatbeltPath(bind.Source, false)
		if err != nil {
			return input, err
		}
		target, err := canonicalSeatbeltPath(bind.Target, false)
		if err != nil || source != target {
			return input, fmt.Errorf("seatbelt cannot remap read-only binds: %q -> %q", bind.Source, bind.Target)
		}
		if err := safeScope(source); err != nil {
			return input, err
		}
		info, err := os.Stat(source)
		if err != nil {
			return input, err
		}
		input.ReadPaths = append(input.ReadPaths, seatbeltReadPath{Path: source, Directory: info.IsDir()})
	}
	for _, path := range []string{"/usr", "/bin", "/sbin", "/System/Library", "/Library/Apple/System/Library", "/etc"} {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			continue
		}
		resolved, err := canonicalSeatbeltPath(path, true)
		if err != nil {
			return input, err
		}
		input.SystemPaths = append(input.SystemPaths, resolved)
	}
	return input, nil
}

func (s *Seatbelt) envelope(request model.SandboxRequest, command []string) (model.CommandSpec, error) {
	if len(command) == 0 {
		return model.CommandSpec{}, fmt.Errorf("%w: sandbox command is empty", model.ErrInvalid)
	}
	// Use /tmp explicitly, never an inherited TMPDIR inside a project or state.
	scratch, err := os.MkdirTemp("/tmp", "marshal-seatbelt-")
	if err != nil {
		return model.CommandSpec{}, fmt.Errorf("%w: create seatbelt scratch: %v", model.ErrUnavailable, err)
	}
	cleanup := func() error { return os.RemoveAll(scratch) }
	failed := true
	defer func() {
		if failed {
			cleanup()
		}
	}()
	scratch, err = canonicalSeatbeltPath(scratch, true)
	if err != nil {
		return model.CommandSpec{}, fmt.Errorf("%w: scratch: %v", model.ErrInvalid, err)
	}
	input, err := normalizeSeatbeltRequest(request, scratch)
	if err != nil {
		return model.CommandSpec{}, fmt.Errorf("%w: seatbelt scope: %v", model.ErrInvalid, err)
	}
	if scratch == input.Request.Worktree || pathWithin(input.Request.Worktree, scratch) {
		return model.CommandSpec{}, fmt.Errorf("%w: scratch lies inside worktree", model.ErrInvalid)
	}
	home := filepath.Join(scratch, "home")
	tmp := filepath.Join(scratch, "tmp")
	for _, path := range []string{home, tmp, filepath.Join(home, ".config")} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return model.CommandSpec{}, err
		}
	}
	for _, path := range request.WritableTmpfs {
		if err := validSeatbeltPath(path); err != nil {
			return model.CommandSpec{}, fmt.Errorf("%w: %v", model.ErrInvalid, err)
		}
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return model.CommandSpec{}, fmt.Errorf("%w: invalid scratch path %q", model.ErrInvalid, path)
		}
		if path != "/tmp" && path != "/home/marshal" && !pathWithin("/home/marshal", path) {
			return model.CommandSpec{}, fmt.Errorf("%w: seatbelt cannot mount tmpfs at %q", model.ErrUnavailable, path)
		}
		mapped := tmp
		if path != "/tmp" {
			mapped = home + strings.TrimPrefix(path, "/home/marshal")
		}
		if err := os.MkdirAll(mapped, 0o700); err != nil {
			return model.CommandSpec{}, err
		}
	}
	profile, parameters, err := seatbeltProfile(input)
	if err != nil {
		return model.CommandSpec{}, fmt.Errorf("%w: seatbelt profile: %v", model.ErrInvalid, err)
	}
	args := []string{"-p", profile}
	for _, parameter := range parameters {
		args = append(args, "-D", parameter)
	}
	args = append(args, "--")
	args = append(args, command...)
	env := []string{"HOME=" + home, "PATH=/usr/bin:/bin"}
	for _, kv := range request.ExtraEnv {
		key, value, ok := strings.Cut(kv, "=")
		if !ok || key == "" || strings.ContainsAny(kv, "\x00\n\r") {
			return model.CommandSpec{}, fmt.Errorf("%w: malformed environment entry", model.ErrInvalid)
		}
		if value == "/home/marshal" || pathWithin("/home/marshal", value) {
			value = home + strings.TrimPrefix(value, "/home/marshal")
		}
		if key == "HOME" {
			value = home
		}
		if key == "TMPDIR" {
			value = tmp
		}
		env = append(env, key+"="+value)
	}
	env = append(env, "TMPDIR="+tmp)
	failed = false
	return model.CommandSpec{Path: s.binary, Args: args, Env: env, Dir: input.Request.Worktree, Cleanup: cleanup}, nil
}
