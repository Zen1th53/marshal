//go:build linux

package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// PTY conformance harness.
//
// Every other test in this package calls Go functions in process. That proves a
// handler exists; it does not prove the shipped binary reacts to a real
// terminal. These tests allocate an actual pseudo-terminal, run the compiled
// marshal binary on it, write raw key bytes, and read what the program draws,
// so a keystroke only counts as implemented when it survives the full path
// through the terminal.
//
// The PTY is opened with raw syscalls rather than a third-party package so the
// suite adds no dependency to the release build.

// openPTY allocates a controlling pseudo-terminal pair.
func openPTY(t *testing.T) (master, slave *os.File) {
	t.Helper()

	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("cannot open /dev/ptmx: %v", err)
	}

	// Unlock the slave side and learn its number.
	var unlock int32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, m.Fd(),
		syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); errno != 0 {
		m.Close()
		t.Skipf("TIOCSPTLCK failed: %v", errno)
	}
	var ptyN uint32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, m.Fd(),
		syscall.TIOCGPTN, uintptr(unsafe.Pointer(&ptyN))); errno != 0 {
		m.Close()
		t.Skipf("TIOCGPTN failed: %v", errno)
	}

	s, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", ptyN), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		m.Close()
		t.Skipf("cannot open pty slave: %v", err)
	}

	t.Cleanup(func() { s.Close(); m.Close() })
	return m, s
}

// setWinsize sets the terminal dimensions so responsive layout can be exercised.
func setWinsize(f *os.File, rows, cols uint16) {
	ws := struct{ Row, Col, X, Y uint16 }{Row: rows, Col: cols}
	syscall.Syscall(syscall.SYS_IOCTL, f.Fd(),
		syscall.TIOCSWINSZ, uintptr(unsafe.Pointer(&ws)))
}

// ptySession drives the compiled binary over a real terminal.
type ptySession struct {
	t      *testing.T
	master *os.File
	cmd    *exec.Cmd

	mu  sync.Mutex
	buf strings.Builder
}

// startTUI builds the marshal binary once, initialises a throwaway project, and
// runs `marshal tui` attached to a pseudo-terminal.
func startTUI(t *testing.T, rows, cols uint16) *ptySession {
	t.Helper()

	bin := buildMarshalBinary(t)
	project := initProject(t, bin)

	master, slave := openPTY(t)
	setWinsize(master, rows, cols)

	cmd := exec.Command(bin, "tui")
	cmd.Dir = project
	cmd.Stdin = slave
	cmd.Stdout = slave
	cmd.Stderr = slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}

	if err := cmd.Start(); err != nil {
		t.Fatalf("start marshal tui on pty: %v", err)
	}
	slave.Close()

	s := &ptySession{t: t, master: master, cmd: cmd}

	go func() {
		chunk := make([]byte, 8192)
		for {
			n, err := master.Read(chunk)
			if n > 0 {
				s.mu.Lock()
				s.buf.Write(chunk[:n])
				s.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()

	t.Cleanup(func() {
		if cmd.Process != nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	})

	// Wait for the workspace to paint before driving input.
	s.waitFor("MARSHAL", 15*time.Second)
	return s
}

func (s *ptySession) output() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// send writes raw bytes to the terminal, exactly as a keyboard would.
func (s *ptySession) send(raw string) {
	s.t.Helper()
	if _, err := s.master.WriteString(raw); err != nil {
		s.t.Fatalf("write to pty: %v", err)
	}
	time.Sleep(120 * time.Millisecond)
}

// sendLine types a line and presses Enter.
func (s *ptySession) sendLine(line string) {
	s.send(line + "\r")
	time.Sleep(350 * time.Millisecond)
}

// waitFor blocks until the substring appears in the terminal output.
func (s *ptySession) waitFor(want string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(s.output(), want) {
			return true
		}
		time.Sleep(80 * time.Millisecond)
	}
	return false
}

func (s *ptySession) mustSee(want string) {
	s.t.Helper()
	if !s.waitFor(want, 8*time.Second) {
		s.t.Fatalf("expected %q on the terminal.\n--- output tail ---\n%s", want, tail(s.output(), 3000))
	}
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

var (
	buildOnce sync.Once
	buildPath string
	buildErr  error
)

// buildMarshalBinary compiles the binary under test once per package run.
func buildMarshalBinary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "marshal-pty-bin-")
		if err != nil {
			buildErr = err
			return
		}
		out := filepath.Join(dir, "marshal")
		cmd := exec.Command("go", "build", "-o", out, "../../cmd/marshal")
		if combined, err := cmd.CombinedOutput(); err != nil {
			buildErr = fmt.Errorf("build marshal: %v: %s", err, combined)
			return
		}
		buildPath = out
	})
	if buildErr != nil {
		t.Fatalf("%v", buildErr)
	}
	return buildPath
}

// initProject creates a git repository with an initialised MARSHAL store. The
// binary refuses to start outside one, so this mirrors real first use.
func initProject(t *testing.T, bin string) string {
	t.Helper()
	dir := t.TempDir()

	run := func(name string, args ...string) {
		cmd := exec.Command(name, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=conformance", "GIT_AUTHOR_EMAIL=conformance@example.invalid",
			"GIT_COMMITTER_NAME=conformance", "GIT_COMMITTER_EMAIL=conformance@example.invalid")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %v: %s", name, args, err, out)
		}
	}

	run("git", "init", "-q", ".")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("conformance\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("git", "add", "-A")
	run("git", "commit", "-qm", "chore: init")
	run(bin, "init")

	return dir
}

// TestPTYWorkspacePaintsRealState proves the shipped binary renders a workspace
// on a real terminal, and that the values shown come from the store.
func TestPTYWorkspacePaintsRealState(t *testing.T) {
	s := startTUI(t, 40, 120)

	s.mustSee("MARSHAL")

	s.sendLine("/goal Prove the terminal is real")
	s.mustSee("Prove the terminal is real")

	// /status must reflect the goal that was just persisted.
	s.sendLine("/status")
	s.mustSee("CANONICAL STATUS DETAIL")
	s.mustSee("Prove the terminal is real")
}

// TestPTYEveryRegisteredCommandDispatches drives every slash command the
// capability registry advertises through the real terminal and fails if any of
// them falls through to the unknown-command branch. A registry entry that names
// a command the binary rejects is a false capability claim.
func TestPTYEveryRegisteredCommandDispatches(t *testing.T) {
	s := startTUI(t, 40, 120)

	seen := map[string]bool{}
	var commands []string
	for _, cap := range GlobalRegistry.All() {
		surface := strings.TrimSpace(cap.TUISurface)
		if !strings.HasPrefix(surface, "/") {
			continue
		}
		name := strings.Fields(surface)[0]
		if seen[name] {
			continue
		}
		seen[name] = true
		commands = append(commands, name)
	}

	if len(commands) == 0 {
		t.Fatal("registry advertises no slash commands")
	}

	for _, cmd := range commands {
		s.sendLine(cmd)
	}

	// Give the last command time to render.
	time.Sleep(700 * time.Millisecond)
	out := s.output()

	var broken []string
	for _, cmd := range commands {
		if strings.Contains(out, fmt.Sprintf("Unknown command %q", cmd)) {
			broken = append(broken, cmd)
		}
	}
	if len(broken) > 0 {
		t.Fatalf("registry advertises %d command(s) the binary does not dispatch: %s",
			len(broken), strings.Join(broken, ", "))
	}
}

// TestPTYKeyboardContract drives the documented control keys as raw bytes.
func TestPTYKeyboardContract(t *testing.T) {
	s := startTUI(t, 40, 120)

	// Seed history so Up has something to recall.
	s.sendLine("/status")

	// Up recalls the previous entry; Ctrl+A jumps home, Ctrl+E to end, and
	// Ctrl+W deletes the preceding word. Ctrl+U clears, leaving a clean line.
	s.send("\x1b[A")  // Up
	s.send("\x01")    // Ctrl+A
	s.send("\x05")    // Ctrl+E
	s.send("\x1b[D")  // Left
	s.send("\x1b[C")  // Right
	s.send("\x17")    // Ctrl+W
	s.send("\x15")    // Ctrl+U (clear line)

	// The session must still be alive and accepting commands after all of that.
	s.sendLine("/status")
	s.mustSee("CANONICAL STATUS DETAIL")

	if s.cmd.ProcessState != nil && s.cmd.ProcessState.Exited() {
		t.Fatal("terminal session died while handling control keys")
	}
}

// TestPTYTabCompletion proves Tab completion happens inside the real terminal
// rather than only in the in-process completer.
func TestPTYTabCompletion(t *testing.T) {
	s := startTUI(t, 40, 120)

	// "/doc" + Tab must resolve to /doctor and then run it.
	s.send("/doc")
	s.send("\t")
	s.send("\r")

	if !s.waitFor("DIAGNOSTICS", 20*time.Second) {
		t.Fatalf("Tab did not complete /doc into a runnable /doctor.\n--- output tail ---\n%s",
			tail(s.output(), 2500))
	}
}

// TestPTYCtrlCDoesNotKillSession proves Ctrl+C is a safe interrupt: it must not
// terminate the durable workspace.
func TestPTYCtrlCDoesNotKillSession(t *testing.T) {
	s := startTUI(t, 40, 120)

	s.send("some partial input")
	s.send("\x03") // Ctrl+C
	time.Sleep(500 * time.Millisecond)

	if s.cmd.ProcessState != nil && s.cmd.ProcessState.Exited() {
		t.Fatal("Ctrl+C terminated the MARSHAL session; it must only interrupt input")
	}

	// The workspace must still serve commands afterwards.
	s.sendLine("/status")
	s.mustSee("CANONICAL STATUS DETAIL")
}

// TestPTYResizeKeepsWorkspaceUsable resizes the terminal underneath a running
// session, including down to the 80x24 floor, and proves it still renders.
func TestPTYResizeKeepsWorkspaceUsable(t *testing.T) {
	s := startTUI(t, 40, 160)
	s.sendLine("/status")
	s.mustSee("CANONICAL STATUS DETAIL")

	for _, dim := range []struct{ rows, cols uint16 }{{24, 80}, {50, 200}, {24, 80}} {
		setWinsize(s.master, dim.rows, dim.cols)
		syscall.Kill(s.cmd.Process.Pid, syscall.SIGWINCH)
		time.Sleep(300 * time.Millisecond)

		s.sendLine("/status")
		if !s.waitFor("CANONICAL STATUS DETAIL", 8*time.Second) {
			t.Fatalf("workspace unusable at %dx%d", dim.cols, dim.rows)
		}
		if s.cmd.ProcessState != nil && s.cmd.ProcessState.Exited() {
			t.Fatalf("session died on resize to %dx%d", dim.cols, dim.rows)
		}
	}
}

// TestPTYNoFabricatedModelNames is the anti-fabrication guard. On a project with
// no registered agents and no configured models, the terminal must not display
// a specific model name it cannot possibly have established.
func TestPTYNoFabricatedModelNames(t *testing.T) {
	s := startTUI(t, 40, 140)

	s.sendLine("/agents")
	s.sendLine("/msg all hello")
	s.sendLine("/agents")
	s.sendLine("/provider status")
	time.Sleep(600 * time.Millisecond)

	out := s.output()
	for _, fabricated := range []string{
		"claude-3-7-sonnet", "claude-3-5-sonnet",
		"gpt-4o", "o3-mini",
		"deepseek-coder",
		"gemini-2.5-pro", "gemini-2.5-flash",
	} {
		if strings.Contains(out, fabricated) {
			t.Errorf("terminal displayed fabricated model %q on a project with no configured model", fabricated)
		}
	}

	// It must also not assert authentication it never verified.
	if strings.Contains(out, "Status: AUTHENTICATED") {
		t.Error("terminal claimed AUTHENTICATED without performing any authentication check")
	}
}
