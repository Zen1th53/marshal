//go:build linux

package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"unsafe"
)

// setupPTY allocates a pseudo-terminal so setup sees a real terminal on stdin,
// which is the only condition under which it asks anything.
func setupPTY(t *testing.T) (master, slave *os.File) {
	t.Helper()
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("cannot open /dev/ptmx: %v", err)
	}
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

// An empty directory is the case an operator actually starts from: no
// repository, no project. Setup must offer both steps, in the order that makes
// the second one possible, and report what the re-check found.
func TestSetupOffersAndCarriesOutBlockingSteps(t *testing.T) {
	dir := t.TempDir()
	// A commit needs an author, and a test must not depend on the machine's
	// global Git configuration.
	t.Setenv("GIT_AUTHOR_NAME", "MARSHAL Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.invalid")
	t.Setenv("GIT_COMMITTER_NAME", "MARSHAL Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.invalid")
	master, slave := setupPTY(t)
	if _, err := master.WriteString("y\ny\ny\n"); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := Execute(context.Background(), dir, []string{"setup"}, slave, &stdout, &stderr); code != 0 {
		t.Fatalf("setup code=%d stderr=%s", code, stderr.String())
	}
	out := stdout.String()

	for _, want := range []string{
		"Initialize a Git repository here? [y/N]",
		"Make an empty first commit as a baseline? [y/N]",
		"Set up MARSHAL for this project? [y/N]",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("setup did not ask %q.\n%s", want, out)
		}
	}
	// The repository step reports honestly that the problem is not gone: a
	// repository without a commit is still not one work can run in. The commit
	// resolves it, and the project step then has a baseline to build on.
	if !strings.Contains(out, "That step ran but the problem remains. This repository has no commits yet.") {
		t.Fatalf("the repository step should report what the re-check found.\n%s", out)
	}
	if strings.Count(out, "Done.") != 2 {
		t.Fatalf("the commit and project steps should each report a confirmed result.\n%s", out)
	}
	if !strings.Contains(out, "This project is set up for MARSHAL.") {
		t.Fatalf("setup should end with a project that is ready.\n%s", out)
	}
	if !strings.Contains(out, "project-execution") {
		t.Fatalf("work should be available once the steps are done.\n%s", out)
	}
	for _, created := range []string{".git", ".marshal"} {
		if _, err := os.Stat(filepath.Join(dir, created)); err != nil {
			t.Fatalf("setup answered yes but %s does not exist: %v", created, err)
		}
	}
}

// A no leaves the directory exactly as it was. Setup asks; it does not insist.
func TestSetupDeclinedChangesNothing(t *testing.T) {
	dir := t.TempDir()
	master, slave := setupPTY(t)
	if _, err := master.WriteString("n\n"); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := Execute(context.Background(), dir, []string{"setup"}, slave, &stdout, &stderr); code != 0 {
		t.Fatalf("setup code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Skipped.") {
		t.Fatalf("a declined step should say so.\n%s", stdout.String())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("setup changed a directory after a no: %v", entries)
	}
}

// Without a terminal there is no one to ask, so setup stays the report it has
// always been. The same holds for `setup status`, which asks for a report by
// name.
func TestSetupWithoutATerminalOnlyReports(t *testing.T) {
	for _, args := range [][]string{{"setup"}, {"setup", "status"}} {
		dir := t.TempDir()
		var stdout, stderr bytes.Buffer
		if code := Execute(context.Background(), dir, args, strings.NewReader("y\ny\n"), &stdout, &stderr); code != 0 {
			t.Fatalf("%v code=%d stderr=%s", args, code, stderr.String())
		}
		if strings.Contains(stdout.String(), "[y/N]") {
			t.Fatalf("%v prompted with no terminal to answer.\n%s", args, stdout.String())
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Fatalf("%v changed the directory: %v", args, entries)
		}
	}
}

// `setup status` is the reporting form even at a terminal.
func TestSetupStatusNeverAsks(t *testing.T) {
	dir := t.TempDir()
	master, slave := setupPTY(t)
	if _, err := master.WriteString("y\n"); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := Execute(context.Background(), dir, []string{"setup", "status"}, slave, &stdout, &stderr); code != 0 {
		t.Fatalf("setup status code=%d stderr=%s", code, stderr.String())
	}
	if strings.Contains(stdout.String(), "[y/N]") {
		t.Fatalf("setup status prompted.\n%s", stdout.String())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("setup status changed the directory: %v", entries)
	}
}
