package tui

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/memory/importer"
	"github.com/Zen1th53/marshal/internal/tmux"
	"golang.org/x/sys/unix"
)

type emptyReads struct{ calls int }

func (r *emptyReads) Read(p []byte) (int, error) {
	r.calls++
	if r.calls > 10000 {
		return 0, io.EOF
	}
	return 0, nil
}
func TestTerminalEmptyReadDoesNotSpin(t *testing.T) {
	r := &emptyReads{}
	term := NewTerminal(r, io.Discard)
	_, err := term.ReadKey()
	if err != io.ErrNoProgress {
		t.Fatalf("empty reader made %d calls: %v", r.calls, err)
	}
	if r.calls > 100 {
		t.Fatal("unbounded empty reads")
	}
}

type finalRead struct{ done bool }

func (r *finalRead) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	return copy(p, "a"), io.EOF
}
func TestTerminalConsumesDataBeforeEOF(t *testing.T) {
	term := NewTerminal(&finalRead{}, io.Discard)
	ev, err := term.ReadKey()
	if err != nil || ev.Rune != 'a' {
		t.Fatalf("lost final input: %v %v", ev, err)
	}
}

func TestViewTmuxCallDoesNotHoldStateLock(t *testing.T) {
	w := NewWorkspace(nil, "test", "test")
	w.workDir = t.TempDir()
	path := filepath.Join(t.TempDir(), "tmux")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexec sleep 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	tmux.SetBinaryPath(path)
	t.Cleanup(tmux.ResetBinaryPath)
	t.Setenv("MARSHAL_TEST_FORCE_TMUX", "1")
	w.tmuxPath = path
	w.tmuxSession = "test"
	w.tmuxActiveWins["worker"] = &activeTmuxAgent{id: "worker", paneID: "%1", window: "worker", isJoined: true}
	done := make(chan struct{})
	go func() { defer close(done); w.handleViewCommand(context.Background(), []string{"hide"}) }()
	time.Sleep(50 * time.Millisecond)
	locked := make(chan struct{})
	go func() { w.tmuxMu.Lock(); w.tmuxMu.Unlock(); close(locked) }()
	select {
	case <-locked:
	case <-time.After(150 * time.Millisecond):
		t.Error("view holds tmuxMu across tmux call")
	}
	<-done
	<-locked
}

func TestRefreshDoesNotHoldWorkspaceLockAcrossGit(t *testing.T) {
	w := NewWorkspace(nil, "test", "test")
	w.workDir = t.TempDir()
	InvalidateProbeCache()
	probeGitMu.Lock()
	done := make(chan struct{})
	go func() { defer close(done); w.RefreshState(context.Background()) }()
	time.Sleep(50 * time.Millisecond)
	locked := make(chan struct{})
	go func() { w.GetUIState(); close(locked) }()
	select {
	case <-locked:
	case <-time.After(150 * time.Millisecond):
		t.Error("refresh holds UI mutex across IO")
	}
	probeGitMu.Unlock()
	<-done
	<-locked
}

func TestNavigationRefreshStormIsCoalesced(t *testing.T) {
	v := testView(t)
	var count atomic.Int32
	release := make(chan struct{})
	entered := make(chan struct{}, 1000)
	v.OnRepaint(func() { count.Add(1); entered <- struct{}{}; <-release })
	v.Open(context.Background())
	<-entered
	for i := 0; i < 100; i++ {
		v.refreshInBackground(context.Background())
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	v.Wait()
	if n := count.Load(); n > 2 {
		t.Fatalf("100 refresh keys spawned %d refreshes", n)
	}
}

func TestCommandDispatchDoesNotWaitForTmux(t *testing.T) {
	w := NewWorkspace(nil, "test", "test")
	w.workDir = t.TempDir()
	path := filepath.Join(t.TempDir(), "tmux")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexec sleep 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	tmux.SetBinaryPath(path)
	t.Cleanup(tmux.ResetBinaryPath)
	t.Setenv("MARSHAL_TEST_FORCE_TMUX", "1")
	w.tmuxPath = path
	w.tmuxSession = "test"
	w.uiEvents = make(chan func(), 64)
	w.repaint = make(chan struct{}, 1)
	start := time.Now()
	w.runCommand(context.Background(), "/view marshal")
	if time.Since(start) > 100*time.Millisecond {
		t.Error("dispatch blocked key loop")
	}
	// Drain results on the same thread that would handle keys and resize.
	for w.commandBusy.Load() {
		select {
		case fn := <-w.uiEvents:
			fn()
		case <-time.After(5 * time.Second):
			t.Fatal("command never completed")
		}
	}
}

func TestControlPreparationAndSubmissionLeaveKeysResponsive(t *testing.T) {
	v := testView(t)
	v.asyncActions = true
	c := &Confirmation{}
	v.configureConfirmation(c)
	release := make(chan struct{})
	started := make(chan struct{}, 2)
	b := Binding{Action: "test", Title: "test", Prepare: func(ctx context.Context) (Target, error) {
		started <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
			return Target{}, ctx.Err()
		}
		return Target{ID: "target"}, nil
	}, Execute: func(ctx context.Context, r ActionRequest) (Outcome, error) {
		started <- struct{}{}
		<-release
		return Outcome{Detail: "complete"}, nil
	}}
	begin := make(chan struct{})
	go func() { c.Begin(context.Background(), b, ActionRequest{}); close(begin) }()
	<-started
	select {
	case <-begin:
	case <-time.After(100 * time.Millisecond):
		t.Error("preparation blocked input")
	}
	c.Cancel()
	close(release)
	v.Wait()
	<-begin
	if c.Phase() != PhaseIdle {
		t.Fatal("cancelled preparation resurrected confirmation")
	}
}

func TestTerminalIdleReaderStopsOnCancellation(t *testing.T) {
	in, out, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	defer out.Close()
	term := NewTerminal(in, io.Discard)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := term.ReadKeyContext(ctx); done <- err }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatal(err)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("idle stdin reader leaked after cancellation")
	}
}

func TestTerminalLargePasteHasLinearReads(t *testing.T) {
	payload := strings.Repeat("x", 1<<20)
	input := &countingReader{Reader: strings.NewReader("\x1b[200~" + payload + "\x1b[201~")}
	term := NewTerminal(input, io.Discard)
	start := time.Now()
	ev, err := term.ReadKey()
	if err != nil || ev.Type != KeyPaste || ev.Paste != payload {
		t.Fatal("large paste corrupted")
	}
	if input.calls > 300 {
		t.Fatalf("1MiB paste made %d reads", input.calls)
	}
	if time.Since(start) > time.Second {
		t.Fatal("paste parsing took over a second")
	}
}

type countingReader struct {
	io.Reader
	calls int
}

func (r *countingReader) Read(p []byte) (int, error) { r.calls++; return r.Reader.Read(p) }

func TestActivityAndOutputStayBounded(t *testing.T) {
	w := NewWorkspace(nil, "test", "test")
	for i := 0; i < 10000; i++ {
		w.RecordActivity(strings.Repeat("message ", 20))
	}
	if len(w.GetUIState().LastOutput) > 64<<10 {
		t.Fatalf("activity grew to %d bytes", len(w.GetUIState().LastOutput))
	}
	output := outputSection(UIState{LastOutput: strings.Repeat("line\n", 10000)}, w.theme, 100)
	if len(output) > maxActivityEvents+1 {
		t.Fatalf("frame formatted %d output rows", len(output))
	}
}

func TestMonitorCancellationInterruptsStuckTmux(t *testing.T) {
	w := NewWorkspace(nil, "test", "test")
	w.workDir = t.TempDir()
	path := filepath.Join(t.TempDir(), "tmux")
	marker := filepath.Join(t.TempDir(), "started")
	script := "#!/bin/sh\ntouch '" + marker + "'\nexec sleep 10\n"
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	tmux.SetBinaryPath(path)
	t.Cleanup(tmux.ResetBinaryPath)
	w.tmuxPath = path
	w.tmuxSession = "test"
	a := &activeTmuxAgent{id: "worker", role: "worker", paneID: "%1", doneChan: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.monitorAgent(ctx, a, w.workDir, nil, nil, nil, nil, nil)
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("monitor did not poll")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-a.doneChan:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("monitor ignores cancellation inside tmux")
	}
	w.tmuxMonitors.Wait()
}

func TestIdleTerminalHasNoReadSpinOrReaderGrowth(t *testing.T) {
	baseline := runtime.NumGoroutine()
	for i := 0; i < 10; i++ {
		in, out, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		term := NewTerminal(in, io.Discard)
		observed := &countingReader{Reader: in}
		term.in = observed
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { defer close(done); term.ReadKeyContext(ctx) }()
		time.Sleep(30 * time.Millisecond)
		cancel()
		select {
		case <-done:
		case <-time.After(300 * time.Millisecond):
			t.Fatal("reader did not stop")
		}
		if observed.calls != 0 {
			t.Fatalf("idle input performed %d reads", observed.calls)
		}
		in.Close()
		out.Close()
	}
	if n := runtime.NumGoroutine(); n > baseline+2 {
		t.Fatalf("idle readers grew goroutines from %d to %d", baseline, n)
	}
}

func TestControlSubmissionIsAsyncAndRunsOnce(t *testing.T) {
	v := testView(t)
	v.asyncActions = true
	c := &Confirmation{}
	v.configureConfirmation(c)
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	b := Binding{Action: "test", Title: "test", Prepare: func(context.Context) (Target, error) { return Target{ID: "target"}, nil }, Execute: func(context.Context, ActionRequest) (Outcome, error) {
		calls.Add(1)
		close(started)
		<-release
		return Outcome{Detail: "complete"}, nil
	}}
	if err := c.Begin(context.Background(), b, ActionRequest{}); err != nil {
		t.Fatal(err)
	}
	v.Wait()
	c.MoveSelection(1)
	start := time.Now()
	if _, err := c.Submit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 100*time.Millisecond {
		t.Error("submission blocked key loop")
	}
	<-started
	for i := 0; i < 100; i++ {
		if _, err := c.Submit(context.Background()); err == nil {
			t.Error("repeated submit accepted")
		}
	}
	close(release)
	v.Wait()
	if calls.Load() != 1 || c.Phase() != PhaseDone {
		t.Fatalf("calls=%d phase=%v", calls.Load(), c.Phase())
	}
}

func TestNavigationOpeningDoesNotWaitForComposition(t *testing.T) {
	w := NewWorkspace(nil, "test", "test")
	w.uiEvents = make(chan func(), 64)
	w.repaint = make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Block the composition snapshot as a slow authority initialization would.
	w.mu.Lock()
	done := make(chan struct{})
	go func() { w.openNavigation(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Error("navigation opening blocked key handling")
	}
	w.mu.Unlock()
	<-done
	w.commandWG.Wait()
	w.navView.Wait()
}

func TestIdleInboxRefreshDoesNotRewriteView(t *testing.T) {
	root := t.TempDir()
	s, err := openStream(root)
	if err != nil {
		t.Fatal(err)
	}
	v, err := openInboxView(root, "codex", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := refreshInboxView(root, v, s); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(v.path)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	for i := 0; i < 10; i++ {
		if err := refreshInboxView(root, v, s); err != nil {
			t.Fatal(err)
		}
	}
	after, err := os.Stat(v.path)
	if err != nil {
		t.Fatal(err)
	}
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("idle refresh rewrote unchanged inbox")
	}
	// An external append invalidates the cache even when the stream object is unchanged.
	other, err := openStream(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.append("claude", "session", importer.Message{Content: "new entry", Role: "assistant"}); err != nil {
		t.Fatal(err)
	}
	if err := refreshInboxView(root, v, s); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(v.path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "new entry") {
		t.Fatal("cache hid an external channel append")
	}
}

func TestIdleHistorySyncDoesNotRewriteIndex(t *testing.T) {
	w := newNativeHistoryWatch(t.TempDir(), t.TempDir())
	w.indexPath = filepath.Join(t.TempDir(), "index.json")
	if err := w.sync(); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(w.indexPath)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	for i := 0; i < 10; i++ {
		if err := w.sync(); err != nil {
			t.Fatal(err)
		}
	}
	after, err := os.Stat(w.indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("idle history sync fsynced unchanged index")
	}
}

func TestNavigationDigitOutsideManifestDoesNotSpin(t *testing.T) {
	v := testView(t)
	ia := *v.nav.ia
	ia.Sections = ia.Sections[:2]
	v.nav.ia = &ia
	v.open = true
	done := make(chan struct{})
	go func() {
		defer close(done)
		v.HandleKey(context.Background(), runeKey('9'))
	}()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("out-of-range section shortcut spins with the navigation lock held")
	}
	if v.nav.sectionIndex() != 0 {
		t.Fatal("invalid shortcut changed selection")
	}
}

func TestInboxCountDoesNotWaitForBlockedWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "view")
	if err := unix.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	buf := make([]byte, 4096)
	for {
		if _, err := unix.Write(fd, buf); err == unix.EAGAIN {
			break
		} else if err != nil {
			t.Fatal(err)
		}
	}
	v := &inboxView{reader: "marshal", path: path}
	done := make(chan struct{})
	go func() {
		defer close(done)
		v.deliver([]streamEntry{{Provider: "claude", Role: "assistant", Text: "hello"}}, channelConfig{})
	}()
	time.Sleep(50 * time.Millisecond)
	counted := make(chan struct{})
	go func() { v.Count(); close(counted) }()
	select {
	case <-counted:
	case <-time.After(100 * time.Millisecond):
		t.Error("state reader waits behind blocked inbox file IO")
	}
	// Change the pathname before freeing pipe capacity so trim reads a regular file.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := unix.Read(fd, buf); err == unix.EAGAIN {
			break
		} else if err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("inbox writer failed to finish after releasing output")
	}
	<-counted
}

func TestCompletionQualificationDoesNotBlockOrMultiplyPerKey(t *testing.T) {
	w := NewWorkspace(nil, "test", "test")
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1")
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte("#!/bin/sh\nexec /bin/sleep 6\n"), 0700); err != nil {
		t.Fatal(err)
	}
	w.uiEvents = make(chan func(), 64)
	w.composer.SetText("/codex f")
	baseline := runtime.NumGoroutine()
	start := time.Now()
	for i := 0; i < 100; i++ {
		w.refreshCompletion()
	}
	if time.Since(start) > 100*time.Millisecond {
		t.Error("completion qualified providers on the input loop")
	}
	if got := runtime.NumGoroutine(); got > baseline+3 {
		t.Errorf("completion spawned per-key probes: %d -> %d", baseline, got)
	}
	w.commandWG.Wait()
}

func TestFileOwnershipContentionIsBounded(t *testing.T) {
	var gate ioGate
	if err := gate.acquire(); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := gate.acquire(); err == nil {
		t.Fatal("concurrent file ownership admitted")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("file ownership wait was unbounded")
	}
	gate.release()
	if err := gate.acquire(); err != nil {
		t.Fatal("file ownership did not recover", err)
	}
	gate.release()
}

func TestTerminalOutputBackpressureHasDeadline(t *testing.T) {
	var fds [2]int
	if err := unix.Pipe2(fds[:], unix.O_NONBLOCK|unix.O_CLOEXEC); err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fds[0])
	defer unix.Close(fds[1])
	buf := make([]byte, 4096)
	for {
		if _, err := unix.Write(fds[1], buf); err == unix.EAGAIN {
			break
		} else if err != nil {
			t.Fatal(err)
		}
	}
	writer := &terminalOutput{fd: fds[1], timeout: 40 * time.Millisecond}
	term := NewTerminal(strings.NewReader(""), writer)
	screen := NewScreen(term)
	start := time.Now()
	screen.Render([]string{"frame"}, 80, 24, 24, 1)
	if time.Since(start) > 250*time.Millisecond {
		t.Fatal("render waits indefinitely for terminal output capacity")
	}
	if screen.prev != nil {
		t.Fatal("failed output established a false screen baseline")
	}
}

func TestDirectTerminalOwnershipOnlyForNativeChildren(t *testing.T) {
	t.Setenv("TMUX", "")
	t.Setenv("MARSHAL_TEST_FORCE_TMUX", "")
	w := NewWorkspace(nil, "test", "test")
	w.terminal.isTerm = true
	for _, command := range []string{"/doctor", "/resume", "/resume run:task", "/continue codex /tmp/project", "/approval inspect approval-id", "/review ver-id", "/sandbox", "/marshal status"} {
		if w.directTerminalCommand(command) {
			t.Errorf("ordinary command owns stdin: %s", command)
		}
	}
	for _, command := range []string{"/codex", "/claude cli", "/resume --last", "/doctor codex", "/marshal chat"} {
		if !w.directTerminalCommand(command) {
			t.Errorf("native child lost stdin ownership: %s", command)
		}
	}
}

func TestCancelledNavigationCompositionDoesNotReopen(t *testing.T) {
	w := NewWorkspace(nil, "test", "test")
	w.uiEvents = make(chan func(), 64)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.mu.Lock()
	w.openNavigation(ctx)
	w.navView.Close()
	w.mu.Unlock()
	w.commandWG.Wait()
	w.navView.Wait()
	if w.navView.IsOpen() {
		t.Fatal("cancelled navigation reopened after composition finished")
	}
}

func TestIdleWorkspaceMonitorPollCountAndGoroutinesStayBounded(t *testing.T) {
	w := NewWorkspace(nil, "test", "test")
	w.workDir = t.TempDir()
	path := filepath.Join(t.TempDir(), "tmux")
	log := filepath.Join(t.TempDir(), "polls")
	t.Setenv("MARSHAL_POLL_LOG", log)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf 'poll\\n' >> \"$MARSHAL_POLL_LOG\"\nprintf '0 0\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	tmux.SetBinaryPath(path)
	t.Cleanup(tmux.ResetBinaryPath)
	w.tmuxPath, w.tmuxSession = path, "test"
	a := &activeTmuxAgent{id: "worker", role: "worker", paneID: "%1", doneChan: make(chan struct{})}
	baseline := runtime.NumGoroutine()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.monitorAgent(ctx, a, w.workDir, nil, nil, nil, nil, nil)
	time.Sleep(1250 * time.Millisecond)
	cancel()
	w.tmuxMonitors.Wait()
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if calls := strings.Count(string(data), "poll\n"); calls < 1 || calls > 2 {
		t.Fatalf("idle monitor performed %d polls in 1.25s", calls)
	}
	if got := runtime.NumGoroutine(); got > baseline+2 {
		t.Fatalf("idle monitor leaked goroutines: %d -> %d", baseline, got)
	}
}

func TestFailedTmuxPollDoesNotDeclareWorkerDead(t *testing.T) {
	w := NewWorkspace(nil, "test", "test")
	w.workDir = t.TempDir()
	path := filepath.Join(t.TempDir(), "tmux")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	tmux.SetBinaryPath(path)
	t.Cleanup(tmux.ResetBinaryPath)
	w.tmuxPath, w.tmuxSession = path, "test"
	a := &activeTmuxAgent{id: "worker", role: "worker", paneID: "%1", state: "working", doneChan: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.monitorAgent(ctx, a, w.workDir, nil, nil, nil, nil, nil)
	time.Sleep(1250 * time.Millisecond)
	select {
	case <-a.doneChan:
		t.Error("failed tmux query falsely ended worker monitoring")
	default:
	}
	cancel()
	w.tmuxMonitors.Wait()
	if strings.Contains(w.GetUIState().LastOutput, "session ended") {
		t.Fatal("failed tmux query reported a false completion")
	}
}

func TestSkillCompletionDoesNotReadFilesOrAuthorityOnInputLoop(t *testing.T) {
	w := NewWorkspace(nil, "test", "test")
	w.uiEvents = make(chan func(), 64)
	w.composer.SetText("/skill install ")
	release := make(chan struct{})
	w.completer.ctx.InstallableSkills = func() []string {
		<-release
		return []string{"local"}
	}
	done := make(chan struct{})
	go func() { defer close(done); w.refreshCompletion() }()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Error("skill completion calls slow authority/file discovery from key handling")
	}
	close(release)
	<-done
	w.commandWG.Wait()
}

func TestCommandHintsDoNotRaceCompletionUpdates(t *testing.T) {
	w := NewWorkspace(nil, "test", "test")
	w.uiEvents = make(chan func(), 64)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			if _, err := w.cmd.Handle(context.Background(), "status"); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	for i := 0; i < 1000; i++ {
		w.completer.ctx.Commands = []string{"/status", "/codex"}
	}
	<-done
}
