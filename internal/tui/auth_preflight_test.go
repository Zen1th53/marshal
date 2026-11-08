package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/tmux"
)

func assertFrameParticipantState(t *testing.T, renderedPlain, agentID, wantState string) {
	t.Helper()
	pattern := regexp.MustCompile(`(?m)^\s*\S+\s+` + regexp.QuoteMeta(agentID) + `\s+` + regexp.QuoteMeta(wantState) + `\s*$`)
	if !pattern.MatchString(renderedPlain) {
		t.Fatalf("expected %s to render as %s in frame:\n%s", agentID, wantState, renderedPlain)
	}
}

func renderPlainFrame(ws *Workspace) string {
	state := ws.GetUIState()
	ws.mu.RLock()
	th := ws.theme
	workDir := ws.workDir
	ws.mu.RUnlock()
	frame := BuildFrame(state, th, workDir, ws.composer, nil, 120, 40)
	var all []string
	all = append(all, frame.Header...)
	all = append(all, frame.Body...)
	all = append(all, frame.Status)
	return StripANSI(strings.Join(all, "\n"))
}

func TestAuthPreflightDefaultProbeBehavior(t *testing.T) {
	t.Cleanup(SetAuthPreflightForTest(nil))

	binDir := t.TempDir()
	invocations := filepath.Join(t.TempDir(), "invocations.log")
	t.Setenv("PATH", binDir+":/bin:/usr/bin")
	t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1")
	InvalidateProbeCache()
	t.Cleanup(InvalidateProbeCache)

	// Providers without a side-effect-free CLI status command must never be invoked.
	for _, name := range []string{"claude", "opencode", "agy"} {
		script := fmt.Sprintf("#!/bin/sh\necho '%s:$*' >> %q\nexit 1\n", name, invocations)
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, provider := range []string{"claude", "opencode", "agy", "antigravity"} {
		if err := defaultAuthPreflight(context.Background(), provider, "", nil); err != nil {
			t.Fatalf("provider %s should skip CLI preflight, got %v", provider, err)
		}
	}
	if data, err := os.ReadFile(invocations); err == nil && len(data) > 0 {
		t.Fatalf("unexpected CLI calls for non-preflight providers: %s", data)
	}

	// 1. Codex exits non-zero on `codex login status` -> not signed in.
	codexPath := filepath.Join(binDir, "codex")
	loggedOutScript := fmt.Sprintf("#!/bin/sh\necho \"codex:$*\" >> %q\nif [ \"$1\" = \"login\" ] && [ \"$2\" = \"status\" ]; then\n  echo 'Not logged in' >&2\n  exit 1\nfi\nexit 0\n", invocations)
	if err := os.WriteFile(codexPath, []byte(loggedOutScript), 0o755); err != nil {
		t.Fatal(err)
	}
	err := defaultAuthPreflight(context.Background(), "codex", codexPath, []string{"CODEX_HOME=" + t.TempDir()})
	if err == nil || err.Error() != "Codex is not signed in - run `codex login`" {
		t.Fatalf("expected signed-out message, got %v", err)
	}
	logged, readErr := os.ReadFile(invocations)
	if readErr != nil || strings.TrimSpace(string(logged)) != "codex:login status" {
		t.Fatalf("unexpected codex preflight argv: %q (%v)", logged, readErr)
	}

	// 2. Codex exits 0 on `codex login status` -> signed in.
	signedInScript := "#!/bin/sh\nif [ \"$1\" = \"login\" ] && [ \"$2\" = \"status\" ]; then\n  echo 'Logged in using ChatGPT'\n  exit 0\nfi\nexit 1\n"
	if err := os.WriteFile(codexPath, []byte(signedInScript), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := defaultAuthPreflight(context.Background(), "codex", codexPath, nil); err != nil {
		t.Fatalf("expected signed-in preflight to pass, got %v", err)
	}

	// 3. Hung process during preflight stays bounded (~2s) and does not hang MARSHAL.
	hungScript := "#!/bin/sh\nwhile :; do sleep 1; done\n"
	if err := os.WriteFile(codexPath, []byte(hungScript), 0o755); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := defaultAuthPreflight(context.Background(), "codex", codexPath, nil); err != nil {
		t.Fatalf("timed-out unresponsive stub without auth output should be inconclusive, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 4*time.Second {
		t.Fatalf("preflight was not bounded: took %s", elapsed)
	}
}

func TestAuthPreflightBlocksMarshalChatAndMarksProviderUnavailable(t *testing.T) {
	_, logFile := setupFakeTmux(t)
	t.Setenv("TMUX", "/tmp/tmux-1000/default,1234,0")
	t.Setenv("MARSHAL_TEST_FORCE_TMUX", "1")

	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "codex"), []byte("#!/bin/sh\necho 'codex 0.100.0'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1")
	InvalidateProbeCache()
	t.Cleanup(InvalidateProbeCache)

	var preflightCalls atomic.Int32
	t.Cleanup(SetAuthPreflightForTest(func(ctx context.Context, provider, binary string, env []string) error {
		preflightCalls.Add(1)
		if canonicalAuthProvider(provider) == "codex" {
			return ErrProviderNotSignedIn
		}
		return nil
	}))

	workDir := t.TempDir()
	_ = fixtureTmuxNamespace(t, workDir)
	ws := NewWorkspace(nil, "test-proj", "auth-chat-session")
	ws.workDir = workDir
	cleanupTmuxWorkspace(t, ws)

	// Ensure codex starts IDLE before preflight fails.
	initialRendered := renderPlainFrame(ws)
	assertFrameParticipantState(t, initialRendered, "codex", "IDLE")

	// 1. Automatic Marshal chat launch on InitTmux must not open a window.
	ws.InitTmux(workDir)
	if preflightCalls.Load() == 0 {
		t.Fatal("expected auth preflight to run before starting Marshal chat")
	}
	ws.tmuxMu.Lock()
	_, chatExists := ws.tmuxActiveWins["marshal-chat"]
	alert := ws.tmuxAlerts["auth:codex"]
	ws.tmuxMu.Unlock()
	if chatExists {
		t.Fatal("marshal-chat window was registered despite failed auth preflight")
	}
	if alert != "codex: not signed in" {
		t.Fatalf("expected tmux auth alert, got %q", alert)
	}

	logBytes, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	chatWin := tmux.ChatWindowName(workDir)
	if strings.Contains(string(logBytes), "new-window") && strings.Contains(string(logBytes), chatWin) {
		t.Fatalf("marshal-chat tmux window was opened despite failed preflight:\n%s", logBytes)
	}

	// 2. TUI frame shows clear auth message and marks codex UNAVAILABLE rather than IDLE,
	// including after RefreshState and even if LastOutput is replaced by another command.
	_ = ws.RefreshState(context.Background())
	rendered := renderPlainFrame(ws)
	if !strings.Contains(rendered, "Codex is not signed in - run `codex login`") {
		t.Fatalf("missing auth message in rendered frame:\n%s", rendered)
	}
	assertFrameParticipantState(t, rendered, "codex", "UNAVAILABLE")

	ws.mu.Lock()
	ws.state.LastCommand = "/status"
	ws.state.LastOutput = "Status checked."
	ws.mu.Unlock()
	renderedAfterOtherCmd := renderPlainFrame(ws)
	if !strings.Contains(renderedAfterOtherCmd, "Codex is not signed in - run `codex login`") {
		t.Fatalf("auth notice disappeared from activity surface after another command:\n%s", renderedAfterOtherCmd)
	}
	assertFrameParticipantState(t, renderedAfterOtherCmd, "codex", "UNAVAILABLE")

	// 3. Explicit /marshal chat with attached runtime also refuses without opening a window.
	_, rtWS, ctx := acceptanceWorkspace(t)
	cleanupTmuxWorkspace(t, rtWS)
	rtWS.tmuxPath, _ = tmux.FindBinary()
	rtWS.tmuxSession, rtWS.tmuxMarshalWin = fixtureTmuxNamespace(t, rtWS.providerRoot()), "marshal"
	if err := saveDefaultProvider(rtWS.providerRoot(), "codex"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logFile, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, chatErr := rtWS.marshalChat(ctx)
	if chatErr == nil || chatErr.Error() != "Codex is not signed in - run `codex login`" {
		t.Fatalf("expected auth error from /marshal chat, got %v", chatErr)
	}
	rtLog, _ := os.ReadFile(logFile)
	if strings.Contains(string(rtLog), "new-window") {
		t.Fatalf("/marshal chat opened a window despite failed preflight:\n%s", rtLog)
	}
	rtRendered := renderPlainFrame(rtWS)
	assertFrameParticipantState(t, rtRendered, "codex", "UNAVAILABLE")
}

func TestAuthPreflightBlocksNativeSessionButAllowsLoginAndRecovers(t *testing.T) {
	_, logFile := setupFakeTmux(t)
	t.Setenv("TMUX", "/tmp/tmux-1000/default,1234,0")
	t.Setenv("MARSHAL_TEST_FORCE_TMUX", "1")

	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "codex"), []byte("#!/bin/sh\necho 'codex 0.100.0'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1")
	InvalidateProbeCache()
	t.Cleanup(InvalidateProbeCache)

	var signedIn atomic.Bool
	t.Cleanup(SetAuthPreflightForTest(func(ctx context.Context, provider, binary string, env []string) error {
		if canonicalAuthProvider(provider) == "codex" && !signedIn.Load() {
			return ErrProviderNotSignedIn
		}
		return nil
	}))

	workDir := t.TempDir()
	session := fixtureTmuxNamespace(t, workDir)
	ws := NewWorkspace(nil, "test-proj", "auth-native-session")
	ws.workDir = workDir
	ws.tmuxPath, _ = tmux.FindBinary()
	ws.tmuxSession, ws.tmuxMarshalWin = session, "marshal"
	cleanupTmuxWorkspace(t, ws)

	ctx := context.Background()
	codexWin := tmux.WindowName("codex", workDir)

	// 1. Native session launch while signed out refuses and opens no window.
	_, err := ws.runNativeAgentInTmux(ctx, nativeLaunchOperator, "codex", "Codex", workDir, "echo", nil, nil, nil, nil, nil, nil, nil, nil)
	if err == nil || err.Error() != "Codex is not signed in - run `codex login`" {
		t.Fatalf("expected native session auth refusal, got %v", err)
	}
	logBytes, _ := os.ReadFile(logFile)
	if strings.Contains(string(logBytes), "new-window -d -t "+session+" -n "+codexWin) {
		t.Fatalf("native codex window was opened while signed out:\n%s", logBytes)
	}
	assertFrameParticipantState(t, renderPlainFrame(ws), "codex", "UNAVAILABLE")

	// 2. Explicit `login` subcommand bypasses auth preflight so the operator can sign in.
	loginCtx := context.WithValue(ctx, nativeRequestedArgsKey{}, []string{"login"})
	msg, err := ws.runNativeAgentInTmux(loginCtx, nativeLaunchOperator, "codex", "Codex", workDir, "echo", []string{"login"}, nil, nil, nil, nil, nil, nil, nil)
	if err != nil || !strings.Contains(msg, "Opened native Codex") {
		t.Fatalf("expected /codex login to bypass preflight, got msg=%q err=%v", msg, err)
	}
	ws.StopAllWorkers(ctx)

	// 3. Once signed in, launching Codex succeeds and restores IDLE state.
	signedIn.Store(true)
	msg, err = ws.runNativeAgentInTmux(ctx, nativeLaunchOperator, "codex", "Codex", workDir, "echo", nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil || !strings.Contains(msg, "Opened native Codex") {
		t.Fatalf("expected signed-in launch to succeed, got msg=%q err=%v", msg, err)
	}
	recoveredFrame := renderPlainFrame(ws)
	assertFrameParticipantState(t, recoveredFrame, "codex", "IDLE")
	ws.tmuxMu.Lock()
	_, stillAlerted := ws.tmuxAlerts["auth:codex"]
	ws.tmuxMu.Unlock()
	if stillAlerted {
		t.Fatal("expected auth alert to clear after successful authentication")
	}
}

func TestAuthPreflightBlocksDispatchedTaskLaunch(t *testing.T) {
	_, logFile := setupFakeTmux(t)
	t.Setenv("TMUX", "/tmp/tmux-1000/default,1234,0")
	t.Setenv("MARSHAL_TEST_FORCE_TMUX", "1")

	t.Cleanup(SetAuthPreflightForTest(func(ctx context.Context, provider, binary string, env []string) error {
		if canonicalAuthProvider(provider) == "codex" {
			return ErrProviderNotSignedIn
		}
		return nil
	}))

	workDir := t.TempDir()
	session := fixtureTmuxNamespace(t, workDir)
	ws := NewWorkspace(nil, "test-proj", "auth-task-session")
	ws.workDir = workDir
	ws.tmuxPath, _ = tmux.FindBinary()
	ws.tmuxSession, ws.tmuxMarshalWin = session, "marshal"
	cleanupTmuxWorkspace(t, ws)
	ws.state.Participants = []model.Participant{
		{AgentID: "codex", Harness: "codex", Role: model.RoleDeveloper, Model: UnknownModel, IsActive: true},
	}

	taskDrv := &tmuxTaskDriver{
		inner: &testMockDriver{},
		w:     ws,
	}
	req := driver.Request{
		RunID: "RUN-auth-1",
		Task: marshal.Task{
			PlanTaskID: "task-auth",
			Worker:     "codex",
		},
		Worktree: workDir,
	}
	h, err := taskDrv.Launch(context.Background(), req)
	if err == nil || err.Error() != "Codex is not signed in - run `codex login`" || h != nil {
		t.Fatalf("expected task preflight refusal, got handle=%v err=%v", h, err)
	}
	logBytes, _ := os.ReadFile(logFile)
	if strings.Contains(string(logBytes), "new-window") {
		t.Fatalf("dispatched task opened a window despite failed preflight:\n%s", logBytes)
	}
	assertFrameParticipantState(t, renderPlainFrame(ws), "codex", "UNAVAILABLE")
}

func TestAuthRuntimeDetectsLoginPromptReconnectLoopAndAuthExit(t *testing.T) {
	setupCustomCaptureTmux := func(t *testing.T, paneContentFile, deadStateFile, logFile string) {
		t.Helper()
		fakeTmux := filepath.Join(t.TempDir(), "tmux")
		script := fmt.Sprintf(`#!/bin/sh
echo "$@" >> %q
case "$1" in
  display-message)
    case "$*" in
      *"#{pane_dead}"*) cat %q; exit 0 ;;
      *"#{pane_id}"*) printf '%%%%1\n'; exit 0 ;;
      *"#{session_name}:#{window_id}"*) printf '%%s:@0\n' "${MARSHAL_FAKE_TMUX_SESSION:-session}"; exit 0 ;;
      *"#{session_id}"*) printf '$0\n'; exit 0 ;;
      *"#{window_active}"*) printf '0\n'; exit 0 ;;
    esac
    exit 0
    ;;
  list-panes)
    printf '%%%%1\t@1\tchat\t101\t0\t0\t\n'
    exit 0
    ;;
  respawn-window|respawn-pane)
    touch %q
    exit 0
    ;;
  capture-pane)
    cat %q
    exit 0
    ;;
  *)
    exit 0
    ;;
esac
`, logFile, deadStateFile, logFile+".restarted", paneContentFile)
		if err := os.WriteFile(fakeTmux, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		tmux.SetBinaryPath(fakeTmux)
		t.Cleanup(tmux.ResetBinaryPath)
	}

	t.Run("interactive_login_prompt", func(t *testing.T) {
		t.Cleanup(SetAuthPreflightForTest(func(context.Context, string, string, []string) error { return nil }))
		dir := t.TempDir()
		paneFile := filepath.Join(dir, "pane.txt")
		deadFile := filepath.Join(dir, "dead.txt")
		logFile := filepath.Join(dir, "tmux.log")
		_ = os.WriteFile(paneFile, []byte("╭──────────────────────────────╮\n│ ❯ 1. Sign in with ChatGPT    │\n│   2. Sign in with Device Code│\n│   3. Provide your own API key│\n╰──────────────────────────────╯\n"), 0o600)
		_ = os.WriteFile(deadFile, []byte("0 0\n"), 0o600)
		setupCustomCaptureTmux(t, paneFile, deadFile, logFile)

		ws := NewWorkspace(nil, "project", "live-login-prompt")
		ws.workDir = dir
		ws.tmuxPath = "tmux"
		ws.tmuxSession, ws.tmuxMarshalWinID, ws.tmuxMarshalPaneID = fixtureTmuxNamespace(t, dir), "@0", "%0"
		ws.state.Participants = []model.Participant{
			{AgentID: "codex", Harness: "codex", Role: model.RoleDeveloper, Model: UnknownModel, IsActive: true},
		}
		cleanupTmuxWorkspace(t, ws)

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		agent := &activeTmuxAgent{
			id: "marshal-chat", role: "marshal-chat", provider: "codex",
			label: "Marshal Chat", window: "chat", windowID: "@1", paneID: "%1",
			state: "working", cancel: cancel, doneChan: make(chan struct{}),
		}
		ws.tmuxActiveWins["marshal-chat"] = agent
		ws.monitorAgent(ctx, agent, dir, nil, nil, nil, nil, nil)

		deadline := time.Now().Add(4 * time.Second)
		for {
			ws.mu.RLock()
			out := ws.state.LastOutput
			ws.mu.RUnlock()
			if strings.Contains(out, "Codex is not signed in - run `codex login`") {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("did not detect interactive login prompt; LastOutput=%q", out)
			}
			time.Sleep(25 * time.Millisecond)
		}
		rendered := renderPlainFrame(ws)
		assertFrameParticipantState(t, rendered, "codex", "UNAVAILABLE")
		ws.tmuxMu.Lock()
		gotState := agent.state
		ws.tmuxMu.Unlock()
		if gotState != "unavailable" {
			t.Fatalf("expected agent state 'unavailable', got %q", gotState)
		}
	})

	t.Run("revoked_refresh_token_loop", func(t *testing.T) {
		t.Cleanup(SetAuthPreflightForTest(func(context.Context, string, string, []string) error { return nil }))
		dir := t.TempDir()
		paneFile := filepath.Join(dir, "pane.txt")
		deadFile := filepath.Join(dir, "dead.txt")
		logFile := filepath.Join(dir, "tmux.log")
		_ = os.WriteFile(paneFile, []byte("ERROR: Failed to refresh token: 401 Unauthorized: Your refresh token was revoked.\nReconnecting... 2/5\n"), 0o600)
		_ = os.WriteFile(deadFile, []byte("0 0\n"), 0o600)
		setupCustomCaptureTmux(t, paneFile, deadFile, logFile)

		ws := NewWorkspace(nil, "project", "live-token-loop")
		ws.workDir = dir
		ws.tmuxPath = "tmux"
		ws.tmuxSession, ws.tmuxMarshalWinID, ws.tmuxMarshalPaneID = fixtureTmuxNamespace(t, dir), "@0", "%0"
		ws.state.Participants = []model.Participant{
			{AgentID: "codex", Harness: "codex", Role: model.RoleDeveloper, Model: UnknownModel, IsActive: true},
		}
		cleanupTmuxWorkspace(t, ws)

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		agent := &activeTmuxAgent{
			id: "codex", role: "worker", provider: "codex",
			label: "Codex", window: "codex", windowID: "@1", paneID: "%1",
			state: "working", cancel: cancel, doneChan: make(chan struct{}),
		}
		ws.tmuxActiveWins["codex"] = agent
		ws.monitorAgent(ctx, agent, dir, nil, nil, nil, nil, nil)

		deadline := time.Now().Add(4 * time.Second)
		for {
			ws.mu.RLock()
			out := ws.state.LastOutput
			ws.mu.RUnlock()
			if strings.Contains(out, "Codex is not signed in - run `codex login`") {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("did not detect revoked refresh token loop; LastOutput=%q", out)
			}
			time.Sleep(25 * time.Millisecond)
		}
		assertFrameParticipantState(t, renderPlainFrame(ws), "codex", "UNAVAILABLE")
	})

	t.Run("auth_exit_retires_marshal_chat_without_restart", func(t *testing.T) {
		t.Cleanup(SetAuthPreflightForTest(func(context.Context, string, string, []string) error { return ErrProviderNotSignedIn }))
		dir := t.TempDir()
		paneFile := filepath.Join(dir, "pane.txt")
		deadFile := filepath.Join(dir, "dead.txt")
		logFile := filepath.Join(dir, "tmux.log")
		_ = os.WriteFile(paneFile, []byte("Error: Failed to refresh token: refresh token was revoked\n"), 0o600)
		_ = os.WriteFile(deadFile, []byte("1 1\n"), 0o600)
		setupCustomCaptureTmux(t, paneFile, deadFile, logFile)

		ws := NewWorkspace(nil, "project", "dead-auth-chat")
		ws.workDir = dir
		ws.tmuxPath = "tmux"
		ws.tmuxSession, ws.tmuxMarshalWinID, ws.tmuxMarshalPaneID = fixtureTmuxNamespace(t, dir), "@0", "%0"
		ws.state.Participants = []model.Participant{
			{AgentID: "codex", Harness: "codex", Role: model.RoleDeveloper, Model: UnknownModel, IsActive: true},
		}
		cleanupTmuxWorkspace(t, ws)

		agent := &activeTmuxAgent{
			id: "marshal-chat", role: "marshal-chat", provider: "codex",
			label: "Marshal Chat", window: "chat", windowID: "@1", paneID: "%1",
			state: "working", doneChan: make(chan struct{}),
		}
		ws.tmuxActiveWins["marshal-chat"] = agent
		ws.monitorAgent(t.Context(), agent, dir, nil, nil, nil, nil, nil)

		select {
		case <-agent.doneChan:
		case <-time.After(4 * time.Second):
			t.Fatal("monitorAgent did not finish after auth exit")
		}
		if _, err := os.Stat(logFile + ".restarted"); !os.IsNotExist(err) {
			t.Fatal("marshal-chat restarted despite auth failure")
		}
		ws.mu.RLock()
		out := ws.state.LastOutput
		ws.mu.RUnlock()
		if !strings.Contains(out, "Codex is not signed in - run `codex login`") || strings.Contains(out, "restarting automatically") {
			t.Fatalf("unexpected LastOutput on auth exit: %q", out)
		}
		assertFrameParticipantState(t, renderPlainFrame(ws), "codex", "UNAVAILABLE")
	})
}
