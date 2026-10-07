package tmux

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	// ErrTmuxMissing indicates the tmux binary could not be found on PATH.
	ErrTmuxMissing = errors.New("tmux binary not found")

	mu           sync.RWMutex
	cachedPath   string
	forceMissing bool
)

// ResetBinaryPath clears the cached tmux binary path and resets missing flag.
func ResetBinaryPath() {
	mu.Lock()
	defer mu.Unlock()
	cachedPath = ""
	forceMissing = false
}

// SetBinaryMissing forces FindBinary to return ErrTmuxMissing (used in tests).
func SetBinaryMissing(missing bool) {
	mu.Lock()
	defer mu.Unlock()
	forceMissing = missing
}

// SetBinaryPath overrides the tmux binary path (used in tests with fake tmux).
func SetBinaryPath(path string) {
	mu.Lock()
	defer mu.Unlock()
	cachedPath = path
	forceMissing = false
}

// FindBinary returns the absolute path to the tmux executable on PATH.
func FindBinary() (string, error) {
	mu.RLock()
	if forceMissing {
		mu.RUnlock()
		return "", ErrTmuxMissing
	}
	if cachedPath != "" {
		p := cachedPath
		mu.RUnlock()
		return p, nil
	}
	mu.RUnlock()

	p, err := exec.LookPath("tmux")
	if err != nil {
		return "", ErrTmuxMissing
	}
	if err := checkVersion(p, "-V"); err != nil {
		return "", err
	}
	if IsInsideTmux() {
		if err := checkVersion(p, "display-message", "-p", "#{version}"); err != nil {
			return "", err
		}
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return p, nil
	}
	mu.Lock()
	defer mu.Unlock()
	if forceMissing {
		return "", ErrTmuxMissing
	}
	if cachedPath == "" {
		cachedPath = abs
	}
	return cachedPath, nil
}

// checkVersion checks both executables and attached servers before using them.
func checkVersion(binary string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.WaitDelay = 100 * time.Millisecond
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("cannot determine tmux version: %w", err)
	}
	version := strings.TrimSpace(strings.TrimPrefix(string(out), "tmux "))
	parts := regexp.MustCompile(`^(?:next-)?([0-9]+)\.([0-9]+)([a-z]*)$`).FindStringSubmatch(version)
	if len(parts) == 4 {
		major, _ := strconv.Atoi(parts[1])
		minor, _ := strconv.Atoi(parts[2])
		if major > 3 || major == 3 && (minor > 3 || minor == 3 && parts[3] >= "a") {
			return nil
		}
	}
	return fmt.Errorf("MARSHAL requires tmux 3.3a or newer (found %q); Ubuntu 22.04 ships 3.2a - use Ubuntu 24.04+, Debian 12+, or build tmux from source", version)
}

// IsInsideTmux reports whether the current process is running inside tmux ($TMUX is set).
func IsInsideTmux() bool {
	return strings.TrimSpace(os.Getenv("TMUX")) != ""
}

// CurrentSessionAndWindow queries tmux for the active session, window name, and window ID.
// It supports tab-delimited formatting to safely handle session or window names with spaces,
// while falling back to whitespace splitting for mock compatibility.
func CurrentSessionAndWindow(ctx context.Context) (session, winName, winID string, err error) {
	args := []string{"display-message", "-p"}
	if pane := os.Getenv("TMUX_PANE"); strings.HasPrefix(pane, "%") {
		args = append(args, "-t", pane)
	}
	out, err := RunCommand(ctx, append(args, "#{session_name}\t#{window_name}\t#{window_id}")...)
	if err != nil || strings.TrimSpace(string(out)) == "" {
		out, err = RunCommand(ctx, append(args, "#{session_name} #{window_name} #{window_id}")...)
		if err != nil {
			return "", "", "", err
		}
	}
	s := strings.TrimSpace(string(out))
	if strings.Contains(s, "\t") {
		parts := strings.Split(s, "\t")
		if len(parts) >= 3 {
			return parts[0], parts[1], parts[2], nil
		}
		if len(parts) == 2 {
			return parts[0], parts[1], "", nil
		}
		if len(parts) == 1 {
			return parts[0], "", "", nil
		}
	}
	parts := strings.Fields(s)
	if len(parts) >= 3 {
		return parts[0], parts[1], parts[2], nil
	}
	if len(parts) == 2 {
		return parts[0], parts[1], "", nil
	}
	if len(parts) == 1 {
		return parts[0], "", "", nil
	}
	return "", "", "", errors.New("empty display-message output")
}

// CurrentPaneID returns the calling terminal's immutable pane identifier.
// Without TMUX_PANE it falls back to querying the selected pane.
func CurrentPaneID(ctx context.Context) (string, error) {
	if pane := os.Getenv("TMUX_PANE"); strings.HasPrefix(pane, "%") {
		return pane, nil
	}
	out, err := RunCommand(ctx, "display-message", "-p", "#{pane_id}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// CurrentWindowID returns the unique tmux window identifier (e.g. @0) for the active window.
func CurrentWindowID(ctx context.Context) (string, error) {
	out, err := RunCommand(ctx, "display-message", "-p", "#{window_id}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// DetectInstallCommand returns a one-line install command for the detected OS distribution.
func DetectInstallCommand() string {
	if runtime.GOOS == "darwin" {
		return "brew install tmux"
	}

	distroID := ""
	for _, releaseFile := range []string{"/etc/os-release", "/usr/lib/os-release"} {
		data, err := os.ReadFile(releaseFile)
		if err == nil {
			distroID = parseOsReleaseID(string(data))
			if distroID != "" {
				break
			}
		}
	}

	switch {
	case strings.Contains(distroID, "arch") || strings.Contains(distroID, "manjaro") || strings.Contains(distroID, "endeavouros"):
		return "sudo pacman -S tmux"
	case strings.Contains(distroID, "ubuntu") || strings.Contains(distroID, "debian") || strings.Contains(distroID, "pop") || strings.Contains(distroID, "mint"):
		return "sudo apt install tmux"
	case strings.Contains(distroID, "fedora") || strings.Contains(distroID, "rhel") || strings.Contains(distroID, "centos") || strings.Contains(distroID, "rocky") || strings.Contains(distroID, "alma"):
		return "sudo dnf install tmux"
	case strings.Contains(distroID, "suse") || strings.Contains(distroID, "opensuse") || strings.Contains(distroID, "sles"):
		return "sudo zypper install tmux"
	}

	if _, err := exec.LookPath("pacman"); err == nil {
		return "sudo pacman -S tmux"
	}
	if _, err := exec.LookPath("apt"); err == nil {
		return "sudo apt install tmux"
	}
	if _, err := exec.LookPath("apt-get"); err == nil {
		return "sudo apt-get install tmux"
	}
	if _, err := exec.LookPath("dnf"); err == nil {
		return "sudo dnf install tmux"
	}
	if _, err := exec.LookPath("zypper"); err == nil {
		return "sudo zypper install tmux"
	}
	if _, err := exec.LookPath("brew"); err == nil {
		return "brew install tmux"
	}

	return "sudo apt install tmux"
}

func parseOsReleaseID(content string) string {
	var id, idLike string
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "ID=") {
			id = strings.ToLower(strings.Trim(strings.TrimPrefix(line, "ID="), `"'`))
		} else if strings.HasPrefix(line, "ID_LIKE=") {
			idLike = strings.ToLower(strings.Trim(strings.TrimPrefix(line, "ID_LIKE="), `"'`))
		}
	}
	return id + " " + idLike
}

// ProjectHash computes a stable, short 8-character hexadecimal hash of the canonical project root.
func ProjectHash(projectRoot string) string {
	resolved := projectRoot
	if r, err := filepath.EvalSymlinks(projectRoot); err == nil {
		resolved = r
	}
	cleaned := filepath.Clean(resolved)
	h := sha256.Sum256([]byte(cleaned))
	return hex.EncodeToString(h[:4])
}

// SessionName returns the dedicated tmux session name for a project.
func SessionName(projectRoot string) string {
	resolved := projectRoot
	if r, err := filepath.EvalSymlinks(projectRoot); err == nil {
		resolved = r
	}
	base := filepath.Base(filepath.Clean(resolved))
	base = sanitizeName(base)
	hash := ProjectHash(projectRoot)
	return fmt.Sprintf("marshal-%s-%s", base, hash)
}

// WindowName returns a window name for a given agent provider and project.
func WindowName(provider, projectRoot string) string {
	hash := ProjectHash(projectRoot)
	return fmt.Sprintf("marshal-%s-%s", sanitizeName(provider), hash)
}

// ChatWindowName returns the dedicated window name for the protected Marshal planning chat.
func ChatWindowName(projectRoot string) string {
	hash := ProjectHash(projectRoot)
	return fmt.Sprintf("marshal-chat-%s", hash)
}

// TaskWindowName returns the dedicated window name for a dispatched task worker.
func TaskWindowName(taskID, projectRoot string) string {
	hash := ProjectHash(projectRoot)
	return fmt.Sprintf("marshal-task-%s-%s", sanitizeName(taskID), hash)
}

func sanitizeName(s string) string {
	var sb strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			sb.WriteRune(r)
		} else {
			sb.WriteRune('-')
		}
	}
	res := strings.Trim(sb.String(), "-")
	if res == "" {
		return "proj"
	}
	return res
}

// EscapeTmuxArg escapes argument tokens for safe passing to tmux commands.
// Tmux's command parser treats ';' or a trailing ';' as a command separator,
// which can cause command injection if user-supplied arguments contain semicolons.
func EscapeTmuxArg(arg string) string {
	if arg == ";" {
		return `\;`
	}
	if strings.HasSuffix(arg, ";") && !strings.HasSuffix(arg, `\;`) {
		return arg[:len(arg)-1] + `\;`
	}
	return arg
}

// EscapeTmuxArgs escapes a slice of arguments for tmux.
func EscapeTmuxArgs(args []string) []string {
	escaped := make([]string, len(args))
	for i, a := range args {
		escaped[i] = EscapeTmuxArg(a)
	}
	return escaped
}

// RunCommand executes a tmux command using the resolved binary path and argv list.
func RunCommand(ctx context.Context, args ...string) ([]byte, error) {
	timeout := 2 * time.Second
	if len(args) > 0 && args[0] == "display-popup" {
		// This client owns an operator review surface until it closes. Honor
		// its explicit review deadline instead of the short query deadline.
		timeout = 30 * time.Second
		if deadline, ok := ctx.Deadline(); ok {
			timeout = time.Until(deadline)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	bin, err := FindBinary()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.WaitDelay = 100 * time.Millisecond
	if os.Getenv("TMUX_TMPDIR") == "" {
		tmpDir := filepath.Join(os.TempDir(), fmt.Sprintf("marshal-tmux-%d", os.Getuid()))
		_ = os.MkdirAll(tmpDir, 0o700)
		cmd.Env = append(os.Environ(), "TMUX_TMPDIR="+tmpDir)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg != "" {
			return nil, fmt.Errorf("tmux %v: %s (%w)", args, errMsg, err)
		}
		return nil, fmt.Errorf("tmux %v: %w", args, err)
	}
	return stdout.Bytes(), nil
}

// requireTarget rejects implicit-current targeting at explicit-target boundaries.
func requireTarget(targets ...string) error {
	for _, target := range targets {
		if strings.TrimSpace(target) == "" {
			return errors.New("empty tmux target refused")
		}
	}
	return nil
}

// resolvePane pins capture/kill to a resolved immutable ID. A successful
// display-message with empty output is not proof that a pane exists.
func resolvePane(ctx context.Context, target string) (string, error) {
	if err := requireTarget(target); err != nil {
		return "", err
	}
	out, err := RunCommand(ctx, "display-message", "-p", "-t", target, "#{pane_id}")
	if err != nil {
		return "", err
	}
	pane := strings.TrimSpace(string(out))
	if !regexp.MustCompile(`^%[0-9]+$`).MatchString(pane) {
		return "", fmt.Errorf("unresolved tmux pane target %q", target)
	}
	return pane, nil
}

// HasSession reports whether a tmux session exists.
func HasSession(ctx context.Context, session string) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := requireTarget(session); err != nil {
		return false
	}
	bin, err := FindBinary()
	if err != nil {
		return false
	}
	cmd := exec.CommandContext(ctx, bin, "has-session", "-t", session)
	cmd.WaitDelay = 100 * time.Millisecond
	if os.Getenv("TMUX_TMPDIR") == "" {
		tmpDir := filepath.Join(os.TempDir(), fmt.Sprintf("marshal-tmux-%d", os.Getuid()))
		_ = os.MkdirAll(tmpDir, 0o700)
		cmd.Env = append(os.Environ(), "TMUX_TMPDIR="+tmpDir)
	}
	return cmd.Run() == nil
}

// NewSession creates a new detached tmux session.
func NewSession(ctx context.Context, session, workDir, windowName string, command []string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	bin, err := FindBinary()
	if err != nil {
		return err
	}
	args := []string{"new-session", "-d", "-s", session, "-c", workDir, "-n", windowName}
	for _, a := range command {
		args = append(args, EscapeTmuxArg(a))
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.WaitDelay = 100 * time.Millisecond
	if os.Getenv("TMUX_TMPDIR") == "" {
		tmpDir := filepath.Join(os.TempDir(), fmt.Sprintf("marshal-tmux-%d", os.Getuid()))
		_ = os.MkdirAll(tmpDir, 0o700)
		cmd.Env = append(os.Environ(), "TMUX_TMPDIR="+tmpDir)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux new-session: %s: %w", string(out), err)
	}
	return nil
}

// AttachSession attaches to an existing tmux session using the provided standard streams.
func AttachSession(session string, stdin io.Reader, stdout, stderr io.Writer) error {
	return AttachSessionContext(context.Background(), session, stdin, stdout, stderr)
}

func AttachSessionContext(ctx context.Context, session string, stdin io.Reader, stdout, stderr io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, 24*time.Hour)
	defer cancel()
	if err := requireTarget(session); err != nil {
		return err
	}
	bin, err := FindBinary()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, bin, "attach-session", "-t", session)
	cmd.WaitDelay = 100 * time.Millisecond
	if os.Getenv("TMUX_TMPDIR") == "" {
		tmpDir := filepath.Join(os.TempDir(), fmt.Sprintf("marshal-tmux-%d", os.Getuid()))
		_ = os.MkdirAll(tmpDir, 0o700)
		cmd.Env = append(os.Environ(), "TMUX_TMPDIR="+tmpDir)
	}
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

// RespawnPane restarts the process in a pane without changing the
// session's current window; respawn-window would select it.
func RespawnPane(ctx context.Context, target string, command []string) error {
	if err := requireTarget(target); err != nil {
		return err
	}
	args := []string{"respawn-pane", "-k", "-t", target}
	if len(command) > 0 {
		command = append([]string{"env"}, command...)
	}
	return runWindowCommand(ctx, args, command)
}

// RespawnWindow reactivates a window in which the command has exited.
func RespawnWindow(ctx context.Context, target string, command []string) error {
	if err := requireTarget(target); err != nil {
		return err
	}
	args := []string{"respawn-window", "-k", "-t", target}
	if len(command) > 0 {
		command = append([]string{"env"}, command...)
	}
	return runWindowCommand(ctx, args, command)
}

// ListWindows lists window names in the given session (or current session if empty).
func ListWindows(ctx context.Context, session string) ([]string, error) {
	args := []string{"list-windows"}
	if session != "" {
		args = append(args, "-t", session)
	}
	args = append(args, "-F", "#{window_name}")
	out, err := RunCommand(ctx, args...)
	if err != nil {
		return nil, err
	}
	var res []string
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			res = append(res, trimmed)
		}
	}
	return res, nil
}

// WindowExists reports whether a window with the given name exists in the session.
func WindowExists(ctx context.Context, session, windowName string) (bool, error) {
	wins, err := ListWindows(ctx, session)
	if err != nil {
		return false, err
	}
	for _, w := range wins {
		if w == windowName {
			return true, nil
		}
	}
	return false, nil
}

// SelectWindow selects the target window in tmux.
func SelectWindow(ctx context.Context, target string) error {
	if err := requireTarget(target); err != nil {
		return err
	}
	// A pane ID alone leaves tmux needing a current client to find the session.
	if strings.HasPrefix(target, "%") {
		out, err := RunCommand(ctx, "display-message", "-p", "-t", target, "#{session_name}:#{window_id}")
		if err != nil {
			return err
		}
		target = strings.TrimSpace(string(out))
	}
	if err := requireTarget(target); err != nil {
		return err
	}
	_, err := RunCommand(ctx, "select-window", "-t", target)
	return err
}

// SelectPane selects the target pane in tmux.
func SelectPane(ctx context.Context, target string) error {
	if err := requireTarget(target); err != nil {
		return err
	}
	_, err := RunCommand(ctx, "select-pane", "-t", target)
	return err
}

// NewWindow creates a detached window without changing the operator's focus.
// Command arguments remain literal, including in the launcher for large commands.
// An optional paneID receives the immutable ID from the creation response.
func NewWindow(ctx context.Context, targetSession, windowName, workDir string, env []string, command []string, paneID ...*string) error {
	args := []string{"new-window", "-d"}
	if targetSession != "" {
		args = append(args, "-t", targetSession)
	}
	if windowName != "" {
		args = append(args, "-n", windowName)
	}
	if workDir != "" {
		args = append(args, "-c", workDir)
	}
	for _, e := range env {
		if e != "" {
			args = append(args, "-e", e)
		}
	}
	if len(paneID) > 0 {
		args = append(args, "-P", "-F", "#{pane_id}")
	}
	return runWindowCommand(ctx, args, command, paneID...)
}

// tmux 3.2a bounds command messages at 16 KiB. Keep large prompts out of
// that transport without changing the provider's argv or interpreting it.
func runWindowCommand(ctx context.Context, args, command []string, paneID ...*string) error {
	size := 0
	for _, arg := range append(append([]string(nil), args...), command...) {
		size += len(arg) + 1
	}
	var launchPath string
	if size > 8<<10 {
		file, err := os.CreateTemp("", "marshal-tmux-launch-*")
		if err != nil {
			return err
		}
		launchPath = file.Name()
		var script strings.Builder
		script.WriteString("#!/bin/sh\nrm -f -- \"$0\"\nexec")
		for _, arg := range command {
			script.WriteString(" '")
			script.WriteString(strings.ReplaceAll(arg, "'", "'\"'\"'"))
			script.WriteString("'")
		}
		script.WriteByte('\n')
		_, writeErr := file.WriteString(script.String())
		closeErr := file.Close()
		if err := errors.Join(writeErr, closeErr); err != nil {
			_ = os.Remove(launchPath)
			return err
		}
		command = []string{"/bin/sh", launchPath}
	}
	args = append(args, EscapeTmuxArgs(command)...)
	out, err := RunCommand(ctx, args...)
	if err == nil && len(paneID) > 0 {
		pane := strings.TrimSpace(string(out))
		if !strings.HasPrefix(pane, "%") {
			return fmt.Errorf("new window returned invalid pane ID %q", pane)
		}
		*paneID[0] = pane
	}
	if err != nil && launchPath != "" {
		_ = os.Remove(launchPath)
	}
	return err
}

// KillWindow terminates a tmux window and its running processes.
func KillWindow(ctx context.Context, target string) error {
	if err := requireTarget(target); err != nil {
		return err
	}
	_, err := RunCommand(ctx, "kill-window", "-t", target)
	return err
}

// KillPane terminates a specific tmux pane.
func KillPane(ctx context.Context, target string) error {
	target, err := resolvePane(ctx, target)
	if err != nil {
		return err
	}
	_, err = RunCommand(ctx, "kill-pane", "-t", target)
	return err
}

// CapturePane captures the plain-text screen output of the pane.
func CapturePane(ctx context.Context, target string) (string, error) {
	target, err := resolvePane(ctx, target)
	if err != nil {
		return "", err
	}
	out, err := RunCommand(ctx, "capture-pane", "-p", "-S", "-", "-t", target)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// SetPaneReadOnly enables or disables input to a pane (-d disables input / view-only, -e enables input).
func SetPaneReadOnly(ctx context.Context, target string, readOnly bool) error {
	if err := requireTarget(target); err != nil {
		return err
	}
	flag := "-e"
	if readOnly {
		flag = "-d"
	}
	_, err := RunCommand(ctx, "select-pane", "-t", target, flag)
	return err
}

// JoinPane joins a pane into the target window (side-by-side if horizontal is true).
func JoinPane(ctx context.Context, source, target string, horizontal bool) error {
	if err := requireTarget(source, target); err != nil {
		return err
	}
	flag := "-v"
	if horizontal {
		flag = "-h"
	}
	_, err := RunCommand(ctx, "join-pane", flag, "-s", source, "-t", target)
	return err
}

// BreakPane breaks a joined pane out into its own window.
func BreakPane(ctx context.Context, target string, newWinName ...string) error {
	if err := requireTarget(target); err != nil {
		return err
	}
	args := []string{"break-pane", "-s", target}
	if len(newWinName) > 0 && newWinName[0] != "" {
		args = append(args, "-n", newWinName[0])
	}
	_, err := RunCommand(ctx, args...)
	return err
}

// SetStatusText updates the status-right text in the tmux status line.
func SetStatusText(ctx context.Context, session, statusText string) error {
	args := []string{"set-option"}
	if session != "" {
		args = append(args, "-t", session)
	}
	args = append(args, "status-right", statusText)
	_, err := RunCommand(ctx, args...)
	return err
}

// BindWindowKey installs a key in a private project table. Session hooks
// activate it only when a MARSHAL terminal is selected; root is untouched.
func BindWindowKey(ctx context.Context, target, table, key string, actionArgs ...string) error {
	if err := requireTarget(target); err != nil {
		return err
	}
	for i := 0; i < len(actionArgs); i++ {
		if actionArgs[i] == "-t" {
			if i+1 == len(actionArgs) {
				return errors.New("missing tmux action target")
			}
			if err := requireTarget(actionArgs[i+1]); err != nil {
				return err
			}
		}
	}
	if _, err := RunCommand(ctx, "set-option", "-p", "-t", target, "@marshal_key_table", table); err != nil {
		return err
	}
	if len(actionArgs) == 3 && actionArgs[0] == "select-window" && actionArgs[1] == "-t" && strings.HasPrefix(actionArgs[2], "%") {
		out, err := RunCommand(ctx, "display-message", "-p", "-t", actionArgs[2], "#{session_name}:#{window_id}")
		if err != nil {
			return err
		}
		actionArgs[2] = strings.TrimSpace(string(out))
		if err := requireTarget(actionArgs[2]); err != nil {
			return err
		}
	}
	// Remove the old catch-all when reusing a table from an earlier run.
	if _, err := RunCommand(ctx, "unbind-key", "-q", "-T", table, "Any"); err != nil {
		return err
	}
	condition := "#{==:#{@marshal_key_table}," + table + "}"
	if _, err := RunCommand(ctx, "bind-key", "-T", table, key, "if-shell", "-F", condition, strings.Join(actionArgs, " "), "send-keys "+key); err != nil {
		return err
	}
	session, err := RunCommand(ctx, "display-message", "-p", "-t", target, "#{session_id}")
	if err != nil {
		return err
	}
	sessionName := strings.TrimSpace(string(session))
	if err := requireTarget(sessionName); err != nil {
		return err
	}
	// Save the user's default once, including when projects share a session.
	if _, err := RunCommand(ctx, "set-option", "-oqF", "-t", sessionName, "@marshal_default_key_table", "#{key-table}"); err != nil {
		return err
	}
	// Unbound keys are forwarded only from the default table. In tmux 3.2a,
	// an Any binding with argument-less send-keys silently discards the key.
	// Let tmux forward input, handle prefixes and enter copy mode natively.
	tableFormat := "#{?@marshal_key_table,#{@marshal_key_table},#{@marshal_default_key_table}}"
	condition = "#{!=:#{key-table}," + tableFormat + "}"
	setDefault := "set-option -F key-table '" + tableFormat + "'"
	activate := "if-shell -F -t '" + sessionName + "' \"" + condition + "\" \"" + setDefault + "\""
	// Remove creation-time handlers left by an earlier runtime. Replacing
	// client key tables inside pane creation is unnecessary: selection hooks
	// and the explicit activation below run after the pane exists.
	for _, hook := range []string{"after-new-window[805]", "after-split-window[805]", "after-kill-pane[805]"} {
		if _, err := RunCommand(ctx, "set-hook", "-u", "-t", sessionName, hook); err != nil {
			return err
		}
	}
	for _, hook := range []string{"after-select-window[805]", "after-select-pane[805]", "session-window-changed[805]", "client-session-changed[805]"} {
		if _, err := RunCommand(ctx, "set-hook", "-t", sessionName, hook, activate); err != nil {
			return err
		}
	}
	_, err = RunCommand(ctx, "if-shell", "-F", "-t", sessionName, condition, setDefault)
	if err != nil {
		return err
	}
	return nil
}

// SetWindowOption sets a window-level option in tmux.
func SetWindowOption(ctx context.Context, target, option, value string) error {
	if err := requireTarget(target); err != nil {
		return err
	}
	_, err := RunCommand(ctx, "set-option", "-w", "-t", target, option, value)
	return err
}

// IsPaneDead checks whether the pane has exited.
func IsPaneDead(ctx context.Context, target string) (bool, error) {
	dead, _, err := PaneDeadStatus(ctx, target)
	return dead, err
}

// PaneDeadStatus checks whether the pane has exited, and returns its dead status and exit code.
func PaneDeadStatus(ctx context.Context, target string) (dead bool, exitCode int, err error) {
	if err := requireTarget(target); err != nil {
		return false, 0, err
	}
	out, err := RunCommand(ctx, "display-message", "-p", "-t", target, "#{pane_dead} #{pane_dead_status}")
	if err != nil {
		return false, 0, err
	}
	parts := strings.Fields(strings.TrimSpace(string(out)))
	if len(parts) == 0 || (parts[0] != "0" && parts[0] != "1") {
		return false, 0, fmt.Errorf("unresolved tmux pane status for %q", target)
	}
	if parts[0] == "1" {
		code := 0
		if len(parts) >= 2 {
			if c, err := strconv.Atoi(parts[1]); err == nil {
				code = c
			}
		}
		return true, code, nil
	}
	return false, 0, nil
}

// SetPaneTitle sets the title of a pane.
func SetPaneTitle(ctx context.Context, target, title string) error {
	if err := requireTarget(target); err != nil {
		return err
	}
	_, err := RunCommand(ctx, "select-pane", "-t", target, "-T", title)
	return err
}

// PanePIDAndPGID returns the PID and PGID of the process running in the pane.
func PanePIDAndPGID(ctx context.Context, target string) (pid, pgid int, err error) {
	if err := requireTarget(target); err != nil {
		return 0, 0, err
	}
	out, err := RunCommand(ctx, "display-message", "-p", "-t", target, "#{pane_pid}")
	if err != nil {
		return 0, 0, err
	}
	pStr := strings.TrimSpace(string(out))
	if p, err := strconv.Atoi(pStr); err == nil && p > 0 {
		pg, _ := syscall.Getpgid(p)
		if pg <= 0 {
			pg = p
		}
		return p, pg, nil
	}
	return 0, 0, fmt.Errorf("invalid pane PID %q", pStr)
}

// KillProcessGroup terminates the process group and process cleanly with SIGTERM then SIGKILL.
func KillProcessGroup(pid, pgid int) {
	if pgid > 1 {
		_ = syscall.Kill(-pgid, syscall.SIGTERM)
		time.Sleep(20 * time.Millisecond)
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	} else if pid > 1 {
		_ = syscall.Kill(pid, syscall.SIGTERM)
		time.Sleep(20 * time.Millisecond)
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

// PaneInfo holds metadata for a discovered tmux pane.
type PaneInfo struct {
	PaneID     string
	WindowID   string
	WindowName string
	PID        int
	Dead       bool
	ExitCode   int
	Title      string
}

// ListPanes lists all panes in the session with their details.
func ListPanes(ctx context.Context, session string) ([]PaneInfo, error) {
	args := []string{"list-panes"}
	if session != "" {
		args = append(args, "-s", "-t", session)
	} else {
		args = append(args, "-a")
	}
	args = append(args, "-F", "#{pane_id}\t#{window_id}\t#{window_name}\t#{pane_pid}\t#{pane_dead}\t#{pane_dead_status}\t#{pane_title}")
	out, err := RunCommand(ctx, args...)
	if err != nil {
		return nil, err
	}
	var res []PaneInfo
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) >= 6 {
			pid, _ := strconv.Atoi(parts[3])
			dead := parts[4] == "1"
			code, _ := strconv.Atoi(parts[5])
			title := ""
			if len(parts) >= 7 {
				title = parts[6]
			}
			res = append(res, PaneInfo{
				PaneID:     parts[0],
				WindowID:   parts[1],
				WindowName: parts[2],
				PID:        pid,
				Dead:       dead,
				ExitCode:   code,
				Title:      title,
			})
		}
	}
	return res, nil
}

// SetWindowStatus changes only the workspace's own window in a shared session.
func SetWindowStatus(ctx context.Context, target, text string) error {
	if err := requireTarget(target); err != nil {
		return err
	}
	if _, err := RunCommand(ctx, "set-option", "-w", "-t", target, "@marshal_status", text); err != nil {
		return err
	}
	for _, option := range []string{"window-status-format", "window-status-current-format"} {
		if err := SetWindowOption(ctx, target, option, "#{window_index}:#{window_name} #{@marshal_status}"); err != nil {
			return err
		}
	}
	return nil
}
