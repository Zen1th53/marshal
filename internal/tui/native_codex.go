package tui

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/memory/importer"
	"github.com/Zen1th53/marshal/internal/project"
	"github.com/Zen1th53/marshal/internal/tmux"
)

// StartWithNativeCodex opens the native session after terminal ownership has
// been established and before the workspace starts reading keys.
func (w *Workspace) StartWithNativeCodex(args []string) {
	w.nativeStartupProvider = "codex"
	copyArgs := append([]string(nil), args...)
	w.nativeOnStart = &copyArgs
}

// nativeArgs parses argv, never shell code. Quoted image paths, configuration
// values and prompts must reach Codex as the operator entered them.
func nativeArgs(s string) ([]string, error) {
	var args []string
	var word strings.Builder
	var quote rune
	escaped, started := false, false
	for _, r := range s {
		switch {
		case escaped:
			word.WriteRune(r)
			escaped = false
		case r == '\\' && quote != '\'':
			escaped, started = true, true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				word.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote, started = r, true
		case r == ' ' || r == '\t' || r == '\n':
			if started {
				args = append(args, word.String())
				word.Reset()
				started = false
			}
		default:
			word.WriteRune(r)
			started = true
		}
	}
	if escaped || quote != 0 {
		return nil, errors.New("unfinished quote or escape in native CLI arguments")
	}
	if started {
		args = append(args, word.String())
	}
	return args, nil
}

// Native mode intentionally uses the operator's real Codex environment. The
// config-free task harness is a separate workflow with different guarantees.
func (w *Workspace) runNativeCodex(ctx context.Context, args []string) (string, error) {
	return w.runNativeAgent(ctx, "codex", args)
}

func (w *Workspace) runNativeAgent(ctx context.Context, provider string, args []string, marshalBrief ...string) (result string, resultErr error) {
	ctx, cancel := context.WithTimeout(ctx, 24*time.Hour)
	defer cancel()
	args = app.NormalizeProviderArgs(provider, args)

	label, homeEnv, homeDir, historyDir := "Codex", "CODEX_HOME", ".codex", "sessions"
	switch provider {
	case "claude":
		label, homeEnv, homeDir, historyDir = "Claude", "CLAUDE_CONFIG_DIR", ".claude", "projects"
	case "opencode":
		label, homeEnv, homeDir, historyDir = "OpenCode", "", "", ""
	case "antigravity":
		label, homeEnv, homeDir, historyDir = "Antigravity", "", "", ""
	case "codex":
	default:
		return "", fmt.Errorf("unsupported native provider %q", provider)
	}
	if (w.terminal == nil || !w.terminal.IsTerminal()) && !tmux.IsInsideTmux() {
		hint := fmt.Sprintf("use /%s exec for batch tasks", provider)
		if provider == "opencode" || provider == "antigravity" {
			hint = fmt.Sprintf("open marshal tui in a terminal, then retry /%s", map[string]string{"opencode": "opencode", "antigravity": "agy"}[provider])
		}
		return "", fmt.Errorf("native %s requires an interactive terminal; %s", label, hint)
	}
	binaryName := provider
	if provider == "antigravity" {
		binaryName = antigravityBinary
	}
	binary, err := project.FindBinary(binaryName)
	if err != nil {
		return "", fmt.Errorf("%w; Install %s and make %s available on PATH, then retry", err, label, binaryName)
	}
	dialect := app.ObserveProviderDialect(ctx, provider)
	if dialect.Operation(app.ProviderArgOperation(provider, args)).Status == app.ProviderUnknown {
		defer func() {
			label := "UNKNOWN — unqualified pass-through: " + dialect.Provider + " " + app.ProviderArgOperation(provider, args)
			result = label + "\n" + result
			if resultErr != nil {
				resultErr = fmt.Errorf("%s: %w", label, resultErr)
			}
		}()
	}
	if err := dialect.Check(app.ProviderArgOperation(provider, args), true); err != nil {
		return "", err
	}
	root := w.workDir
	if w.runtime != nil {
		root = w.runtime.ProjectRoot()
	}
	var watch *nativeHistoryWatch
	if provider == "opencode" {
		watch = newOpenCodeHistoryWatch(binary, root)
	} else if provider == "antigravity" {
		watch, err = newAntigravityHistoryWatch(root)
		if err != nil {
			return "", err
		}
	} else {
		home := os.Getenv(homeEnv)
		if home == "" {
			userHome, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			home = filepath.Join(userHome, homeDir)
		}
		if !filepath.IsAbs(home) {
			home = filepath.Join(root, home)
		}
		historyPath := filepath.Join(home, historyDir)
		if provider == "claude" {
			historyPath, err = providerHistoryDir(provider, root)
			if err != nil {
				return "", err
			}
		}
		watch = newNativeHistoryWatch(historyPath, root)
		watch.claude = provider == "claude"
	}
	if w.runtime != nil {
		w.guardHistoryWatch(watch, provider)
	}
	watch.captureTools = true
	var syncErr error
	watch.indexPath = filepath.Join(root, ".marshal", provider, "history-index.json")
	if err := watch.loadIndex(); err != nil {
		syncErr = err
	}
	if provider == "opencode" {
		// Establish a baseline before the child runs. The exit sync then imports
		// only the session created or updated by this launch, rather than every
		// historical OpenCode session already present on the machine.
		if err := watch.primeOpenCode(); err != nil {
			syncErr = joinNativeSyncError(syncErr, err)
		}
	}
	if provider == "antigravity" {
		// The same baseline, for the same reason: agy keeps every conversation
		// the machine has ever held, and only this launch's belong here.
		if err := watch.primeAntigravity(); err != nil {
			syncErr = joinNativeSyncError(syncErr, err)
		}
	}
	imported := 0
	if source := w.controlSource(); source != nil {
		if authority, ok := source.Authority.(*runtimeControlAuthority); ok {
			if (provider == "codex" && nativeUsesModelPreference(args)) || (provider == "claude" && claudeUsesModelPreference(args)) {
				var selected string
				var err error
				if provider == "claude" {
					selected, err = authority.SelectedClaudeModel(ctx)
				} else {
					selected, err = authority.SelectedCodexModel(ctx)
				}
				if err == nil && selected != "" {
					args = append([]string{"--model", selected}, args...)
				}
			}
			watch.consume = func(tr importer.SessionTranscript) error {
				records, dropped, err := authority.runtime.ProposeContinuation(context.WithoutCancel(ctx), tr)
				if err != nil {
					return err
				}
				for _, rec := range records {
					// Existing approved records do not need another write.
					if _, err := authority.runtime.Store().GetMemoryV2(ctx, authority.runtime.ProjectID(), rec.ID); err == nil {
						continue
					}
					imported++
				}
				if dropped {
					w.mu.Lock()
					w.state.LastOutput = "Secrets were dropped from captured history."
					w.mu.Unlock()
				}
				return nil
			}
		}
	}
	if provider == "opencode" && openCodeUsesModelPreference(args) {
		if selected := strings.TrimSpace(os.Getenv("MARSHAL_OPENCODE_MODEL")); selected != "" {
			args = append([]string{"--model", selected}, args...)
		}
	}
	if watch.consume == nil {
		return "", fmt.Errorf("native %s memory capture requires an attached runtime; open marshal tui in an initialized project (marshal init), then retry", label)
	}

	// The shared channel. Who joins it and who each agent sees in it are the
	// operator's decisions, made before the work starts; an unreadable file
	// means the default, because a missing preference is not a decision.
	channelCfg, channelProblems := loadChannelConfig(root)

	chStream, streamErr := openStream(root)
	if streamErr != nil {
		syncErr = joinNativeSyncError(syncErr, fmt.Errorf("open channel: %w", streamErr))
	}

	// Drop what this session does into the channel as it happens. One entry,
	// whoever ends up reading it: the readers filter when they read, so nothing
	// here depends on who is running.
	captureTranscript := watch.consume
	if chStream != nil && channelCfg.joins(provider) {
		own := captureTranscript
		watch.consume = func(tr importer.SessionTranscript) error {
			if err := own(tr); err != nil {
				return err
			}
			return publishNativeTranscript(chStream, provider, tr)
		}
	}

	// This agent's own view of the channel, and the cursor saying how far it
	// has already looked.
	view, viewErr := openInboxView(root, provider, true)
	if viewErr != nil {
		syncErr = joinNativeSyncError(syncErr, fmt.Errorf("open channel view: %w", viewErr))
	}
	positions, cursorErr := loadCursors(root)
	if cursorErr != nil {
		syncErr = joinNativeSyncError(syncErr, cursorErr)
	}
	// Render whatever flowed past while this agent was closed, before it starts
	// work. This is the difference between joining a conversation and being
	// handed a summary of one.
	drainChannel := func() {
		if chStream == nil || view == nil {
			return
		}
		entries, err := chStream.since(positions[provider])
		if err != nil {
			syncErr = joinNativeSyncError(syncErr, err)
			return
		}
		if len(entries) == 0 {
			return
		}
		if err := refreshInboxView(root, view, chStream); err != nil {
			syncErr = joinNativeSyncError(syncErr, err)
			return
		}
		positions[provider] = entries[len(entries)-1].Seq
		if err := saveCursors(root, positions); err != nil {
			syncErr = joinNativeSyncError(syncErr, err)
		}
	}
	drainChannel()

	// Watch the other agents that capture live, so their work reaches the
	// channel while they run. This also covers an agent running outside
	// MARSHAL, which drops nothing in of its own.
	var peers []*nativeHistoryWatch
	if chStream != nil {
		for _, peer := range knownProviders {
			if peer == provider || !channelCfg.joins(peer) || !capturesLive(peer) {
				continue
			}
			pw, err := newPeerHistoryWatch(peer, root)
			if err != nil {
				syncErr = joinNativeSyncError(syncErr, err)
				continue
			}
			if w.runtime != nil {
				w.guardHistoryWatch(pw, peer)
			}
			pw.captureTools = true
			// A separate index: two MARSHAL sessions watching the same provider
			// must not fight over one file, and this watcher's progress is not
			// that provider's own import progress.
			pw.indexPath = filepath.Join(root, ".marshal", provider, "peer-"+peer+"-index.json")
			if err := pw.loadIndex(); err != nil {
				syncErr = joinNativeSyncError(syncErr, err)
			}
			// Baseline the first run against what is already on disk. Without
			// this a fresh index means the first poll drops the project's whole
			// history into the channel — hundreds of messages, all of them
			// already in durable memory and already summarised in the briefing —
			// and every reader's view fills with them. The channel is for work
			// happening now; the backlog it carries is the backlog it collected,
			// not one replayed into it at startup.
			if pw.indexEmpty() {
				real := pw.consume
				pw.consume = func(importer.SessionTranscript) error { return nil }
				if err := pw.sync(); err != nil {
					syncErr = joinNativeSyncError(syncErr, err)
				}
				pw.consume = real
			}
			peerName := peer
			primary := captureTranscript
			pw.consume = func(tr importer.SessionTranscript) error {
				if err := primary(tr); err != nil {
					return err
				}
				return publishNativeTranscript(chStream, peerName, tr)
			}
			peers = append(peers, pw)
		}
	}

	// Hand this agent what the other providers already did here. A failure to
	// compile or deliver the briefing is reported but never blocks the session:
	// an agent with no briefing is the previous behaviour, not a broken one.
	var briefingNotes []string
	for _, problem := range channelProblems {
		briefingNotes = append(briefingNotes, "live-peers: "+problem)
	}
	// A provider that reads instructions from files gets a directory of
	// MARSHAL's own for this launch, created only when a briefing needs it and
	// removed when the session ends.
	var dir *briefingDir
	defer func() {
		if !w.isTmuxActive() {
			dir.remove()
		}
	}()
	deliver := func(briefing string, channel injectChannel) (string, error) {
		if channel != injectMarshalDir {
			updated, note, err := applyBriefing(provider, root, args, briefing, channel)
			if err == nil {
				args = updated
			}
			return note, err
		}
		if dir == nil {
			created, err := newBriefingDir(root, provider)
			if err != nil {
				return "", err
			}
			dir = created
		}
		return dir.add(briefing)
	}
	channel, fallbackNote := resolveInjectChannel(provider, loadInjectChannel(root))
	if fallbackNote != "" {
		briefingNotes = append(briefingNotes, fallbackNote)
	}
	// Briefings no longer go into the project's AGENTS.md or CLAUDE.md unless
	// the person chose that channel, so a block an earlier version left there
	// is removed rather than going stale in their repository.
	if channel != injectProjectDoc {
		if cleared, err := clearProjectDocBlock(root); err != nil {
			briefingNotes = append(briefingNotes, "Old MARSHAL memory block not removed: "+err.Error())
		} else if cleared > 0 {
			briefingNotes = append(briefingNotes, "Removed MARSHAL's old memory block from the project documents.")
		}
	}
	if channel != injectOff {
		briefing, err := w.crossAgentBriefing(ctx, provider)
		switch {
		case err != nil:
			briefingNotes = append(briefingNotes, "Cross-agent briefing unavailable: "+err.Error())
		case strings.TrimSpace(briefing) == "":
			// Nothing to summarize yet, but another agent may still start while
			// this one runs, so the inbox pointer is delivered on its own.
			if _, err := deliver(briefingHeader+inboxBriefingNote(root, provider), channel); err != nil {
				briefingNotes = append(briefingNotes, "Live inbox pointer not delivered: "+err.Error())
			} else {
				briefingNotes = append(briefingNotes,
					"No other provider has recorded work here yet; live updates will arrive in this session's inbox.")
			}
		case channel == injectPrompt && hasOperatorPrompt(args):
			briefingNotes = append(briefingNotes, "Cross-agent briefing skipped: this session already carries its own prompt.")
		default:
			// The agent only benefits from the inbox if the briefing says it
			// exists, so the pointer travels with the snapshot it will go stale
			// against.
			briefing += inboxBriefingNote(root, provider)
			if note, err := deliver(briefing, channel); err != nil {
				briefingNotes = append(briefingNotes, "Cross-agent briefing not delivered: "+err.Error())
			} else {
				briefingNotes = append(briefingNotes, note)
			}
		}
	}
	if len(marshalBrief) > 0 {
		// The protocol is mandatory and must never become a conversation turn.
		if hiddenChannel(provider) == injectOff || strings.TrimSpace(marshalBrief[0]) == "" {
			return "", fmt.Errorf("Marshal not started: %s has no hidden instruction channel", label)
		}
		if err := scrubMarshalInboxes(root); err != nil {
			return "", fmt.Errorf("Marshal not started: retained briefing cleanup failed: %w", err)
		}
		note, err := deliver(marshalBrief[0], hiddenChannel(provider))
		if err != nil {
			return "", fmt.Errorf("Marshal not started: hidden instruction delivery failed: %w", err)
		}
		briefingNotes = append(briefingNotes, note)
		args = append(args, marshalKickoffArgs(provider)...)
	}
	args, briefingEnv, err := dir.launch(args)
	if err != nil {
		if len(marshalBrief) > 0 {
			return "", fmt.Errorf("Marshal not started: hidden instruction delivery failed: %s", RedactContent(err.Error(), nil))
		}
		return "", fmt.Errorf("Session not started: hidden instruction delivery failed: %s", RedactContent(err.Error(), nil))
	}

	if w.isTmuxActive() {
		if w.navView != nil {
			w.navView.Close()
		}
		isChat := len(marshalBrief) > 0
		if isChat && watch != nil {
			saved := loadChatBinding(root)
			if canonicalNeutralProvider(saved.Provider) != canonicalNeutralProvider(provider) {
				saved.Provider = provider
				saved.SessionID = ""
				saved.HistoryBaseline = nil
			}
			baseline, err := w.prepareChatHistoryWatch(root, watch, saved.SessionID)
			if err != nil {
				return "", err
			}
			saved.HistoryBaseline = baseline
			if err := saveChatBinding(root, saved); err != nil {
				return "", err
			}
		}
		if !isChat && watch != nil && watch.consume != nil {
			prevConsume := watch.consume
			watch.consume = func(tr importer.SessionTranscript) error {
				if tr.SessionID != "" {
					w.tmuxMu.Lock()
					if a := w.tmuxActiveWins[provider]; a != nil {
						a.sessionID = tr.SessionID
					}
					w.tmuxMu.Unlock()
				}
				return prevConsume(tr)
			}
		}
		// This entry point serves operator commands, startup sessions and conversation selection.
		// Runtime-dispatched workers instead enter tmuxTaskDriver.Launch.
		return w.runNativeAgentInTmux(ctx, nativeLaunchOperator, provider, label, root, binary, args, briefingEnv, dir, watch, peers, chStream, view, briefingNotes, isChat)
	}

	if w.navView != nil {
		w.navView.Close()
	}
	resume := w.SuspendTerminal()
	defer resume()
	// The Marshal model takes a moment to start under its briefing. Show the
	// MARSHAL wordmark while it does, so the person sees MARSHAL taking the
	// helm rather than a pause. It plays only for the Marshal launch, which is
	// the one that carries a briefing.
	if len(marshalBrief) > 0 {
		animate := w.theme.AnimationEnabled && w.terminal != nil && w.terminal.IsTerminal()
		playMarshalSplash(os.Stdout, w.theme, animate)
		// The Marshal is presented as MARSHAL, never as the model behind it, so
		// this line does not name the provider the way a plain native session
		// does.
		fmt.Fprintln(os.Stdout, "MARSHAL · the Marshal is planning with you · conversation and tool calls become candidates for operator memory review · exit to return to MARSHAL")
	} else {
		fmt.Fprintf(os.Stdout, "MARSHAL · native %s · conversation and tool calls become candidates for operator memory review · exit to return to MARSHAL\n", label)
	}
	for _, note := range briefingNotes {
		if note != "" {
			fmt.Fprintf(os.Stdout, "MARSHAL · %s\n", RedactContent(note, nil))
		}
	}
	// Published on every pass so a long session shows capture working rather
	// than only reporting once it is over.
	publishStatus := func() {
		status := liveStatus{Imported: imported, Delivered: view.Count(), LastSync: time.Now().UTC()}
		if syncErr != nil {
			status.Error = syncErr.Error()
		}
		// A status file that cannot be written is not worth failing a session
		// over, and the session's own result already reports capture errors.
		_ = writeLiveStatus(root, provider, status)
	}
	publishStatus()

	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.WaitDelay = 100 * time.Millisecond
	cmd.Dir = root
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = append(os.Environ(), briefingEnv...)
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start native %s: %w", label, err)
	}
	w.nativeProvider = provider
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case runErr := <-done:
			if err := watch.sync(); err != nil {
				syncErr = err
			}
			for _, pw := range peers {
				if err := pw.sync(); err != nil {
					syncErr = joinNativeSyncError(syncErr, err)
				}
			}
			drainChannel()
			publishStatus()
			// The command is what the operator types, which for agy is not
			// the provider's long name.
			command := provider
			if provider == "antigravity" {
				command = antigravityBinary
			}
			result := fmt.Sprintf("%s exited. %d message(s), including tool calls, proposed for operator memory review.\n/%s continue resumes; /%s new starts a new session.", label, imported, command, command)
			if delivered := view.Count(); delivered > 0 {
				result += fmt.Sprintf("\n%d channel entr%s from other agents were shown to this session.", delivered, plural(delivered, "y", "ies"))
			}
			for _, note := range briefingNotes {
				if note != "" {
					result += "\n" + note
				}
			}
			if syncErr != nil {
				result += "\nMemory capture incomplete:\n" + syncErr.Error()
			}
			return result, runErr
		case <-ticker.C:
			// OpenCode's public history API is a CLI export backed by the same
			// database the child is using. Export once the child exits; polling it
			// here can delay interactive input and contend with the live session.
			// Antigravity is different: its conversation databases are read
			// read-only and SQLite serves readers under WAL, so polling them costs
			// the running child nothing.
			if capturesLive(provider) {
				if err := watch.sync(); err != nil {
					syncErr = err
				}
			}
			for _, pw := range peers {
				if err := pw.sync(); err != nil {
					syncErr = joinNativeSyncError(syncErr, err)
				}
			}
			drainChannel()
			publishStatus()
		}
	}
}

// joinNativeSyncError keeps a repeated polling failure from filling the result
// pane with the same diagnostic every two seconds. The first occurrence stays
// visible; distinct failures are still reported.
func joinNativeSyncError(existing, next error) error {
	if next == nil {
		return existing
	}
	if existing == nil {
		return next
	}
	if strings.Contains(existing.Error(), next.Error()) {
		return existing
	}
	return errors.Join(existing, next)
}

// hasOperatorPrompt reports whether argv already carries the operator's own
// opening prompt. Appending a briefing after one would hand the CLI a second
// positional, so prompt-channel injection stands down instead.
func hasOperatorPrompt(args []string) bool {
	for _, arg := range args {
		if arg == "--" || arg == "--prompt" || strings.HasPrefix(arg, "--prompt=") ||
			arg == "--prompt-interactive" || arg == "-i" || strings.HasPrefix(arg, "--prompt-interactive=") {
			return true
		}
	}
	return false
}

func nativeUsesModelPreference(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if arg == "-m" || arg == "--model" || strings.HasPrefix(arg, "--model=") || arg == "-p" || arg == "--profile" {
			return false
		}
	}
	// These configuration aliases open sessions too, so they inherit the
	// project model preference just like a bare /codex session.
	if len(args) == 2 && (args[0] == "--sandbox" || args[0] == "-c" && args[1] == `web_search="disabled"`) {
		return true
	}
	return len(args) == 0 || oneOf(args[0], "--", "resume", "fork", "--search")
}

type nativeHistoryWatch struct {
	authorized func(string) bool
	claude     bool
	// openCodeRun is set for OpenCode's SQLite-backed history. Its public CLI
	// supplies JSON exports, so MARSHAL never reads the database or depends on
	// its private schema. The adapter selects visible conversation fields.
	openCodeRun func(args ...string) ([]byte, error)
	// openCodeDB is the store read while a session is open. The CLI export
	// above remains what runs at exit and stays the source of record.
	openCodeDB string
	// antigravity is set for agy's per-conversation SQLite history, which is
	// read directly and read-only: agy has no export command. The adapter
	// takes only fields observed to carry visible conversation and tool
	// evidence, and never reads the model's reasoning.
	antigravity          bool
	antigravitySummaries string
	// captureTools records tool calls and their results alongside conversation,
	// so a later session can see what the agent actually ran and changed rather
	// than only what it said about it.
	captureTools   bool
	dir, root      string
	indexPath      string
	indexDirty     bool
	indexSaved     bool
	seen           map[string]string
	consume        func(importer.SessionTranscript) error
	observeSession func(importer.SessionTranscript) error
}

func newNativeHistoryWatch(dir, root string) *nativeHistoryWatch {
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	return &nativeHistoryWatch{dir: dir, root: filepath.Clean(root), seen: make(map[string]string)}
}

func (w *nativeHistoryWatch) sync() error {
	if w.authorized != nil && !w.authorized(w.dir) {
		return nil
	}
	if w.openCodeDB != "" {
		err := w.syncOpenCodeLive()
		// A store that is not the shape MARSHAL reads is not a failure, it is a
		// reason to use the supported interface instead. The export is slower
		// and cannot run mid-session, so the work arrives at exit rather than as
		// it happens — late, but never wrong and never missing.
		if errors.Is(err, errOpenCodeSchemaMoved) && w.openCodeRun != nil {
			w.openCodeDB = ""
			return w.syncOpenCode()
		}
		return err
	}
	if w.openCodeRun != nil {
		return w.syncOpenCode()
	}
	if w.antigravity {
		return w.syncAntigravity()
	}
	var failures []error
	walkErr := filepath.WalkDir(w.dir, func(path string, entry fs.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		stamp := fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano())
		if w.seen[path] == stamp {
			return nil
		}
		if err := w.syncFile(path); err != nil {
			// One damaged or rejected session must not stop all other sessions.
			if len(failures) < 3 {
				failures = append(failures, fmt.Errorf("%s: %w", filepath.Base(path), err))
			}
			return nil
		}
		w.seen[path] = stamp
		w.indexDirty = true
		return nil
	})
	return errors.Join(append(failures, walkErr, w.saveIndexIfChanged())...)
}

func (w *nativeHistoryWatch) loadIndex() error {
	if w.indexPath == "" {
		return nil
	}
	f, err := os.Open(w.indexPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	var seen map[string]string
	if err := json.NewDecoder(io.LimitReader(f, 1<<20)).Decode(&seen); err != nil {
		return err
	}
	if seen != nil {
		w.seen = seen
	}
	w.indexSaved = true
	return nil
}

func (w *nativeHistoryWatch) saveIndex() error {
	if w.indexPath == "" {
		return nil
	}
	dir := filepath.Dir(w.indexPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".history-index-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	err = json.NewEncoder(f).Encode(w.seen)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(f.Name(), w.indexPath); err != nil {
		return err
	}
	w.indexDirty = false
	w.indexSaved = true
	return nil
}

func (w *nativeHistoryWatch) syncFile(path string) error {
	if w.authorized != nil && !w.authorized(path) {
		return nil
	}
	if w.claude {
		return w.syncClaudeFile(path)
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<20)
	meta, err := r.ReadSlice('\n')
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return err
	}
	meta = bytes.Clone(meta)
	adapter := importer.CodexJSONLAdapter{CaptureTools: w.captureTools}
	tr, err := adapter.Decode(meta)
	if err != nil {
		return err
	}
	tr.Provider = "codex"
	cwd := tr.CWD
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = resolved
	}
	if tr.SessionID == "" || filepath.Clean(cwd) != w.root {
		return nil
	}
	if w.observeSession != nil {
		if err := w.observeSession(tr); err != nil {
			return err
		}
	}
	for {
		line, readErr := r.ReadSlice('\n')
		if errors.Is(readErr, bufio.ErrBufferFull) {
			// Tool payloads can exceed the provider adapter's one-line bound.
			// Drain that event and continue with later conversation instead of
			// making the entire live history permanently unimportable.
			for errors.Is(readErr, bufio.ErrBufferFull) {
				_, readErr = r.ReadSlice('\n')
			}
			if readErr == nil {
				continue
			}
		}
		// Ignore a partial final event; a later poll retries it after append.
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return readErr
		}
		item, err := adapter.Decode(line)
		if err != nil {
			return err
		}
		if item.SessionID != "" && item.SessionID != tr.SessionID {
			return errors.New("mixed session IDs")
		}
		tr.Messages = append(tr.Messages, item.Messages...)
		if len(tr.Messages) >= 64 {
			if err := w.consume(tr); err != nil {
				return err
			}
			tr.Messages = nil
		}
	}
	if len(tr.Messages) != 0 {
		return w.consume(tr)
	}
	return nil
}

// newPeerHistoryWatch builds a watcher for another agent's history.
//
// Each provider keeps its history its own way, and the watcher has to be built
// for the one it is reading: Claude and Codex write files under a directory,
// Antigravity keeps a SQLite database per conversation. A provider that cannot
// be read while it runs has no peer watcher at all — its work reaches the
// channel when its own session ends.
func newPeerHistoryWatch(peer, root string) (*nativeHistoryWatch, error) {
	if !capturesLive(peer) {
		return nil, fmt.Errorf("%s is not read while it runs", peer)
	}
	switch peer {
	case "antigravity":
		return newAntigravityHistoryWatch(root)
	case "opencode":
		dbPath, err := openCodeDBPath()
		if err != nil {
			return nil, err
		}
		watch := newNativeHistoryWatch(filepath.Dir(dbPath), root)
		watch.openCodeDB = dbPath
		return watch, nil
	}
	dir, err := providerHistoryDir(peer, root)
	if err != nil {
		return nil, err
	}
	watch := newNativeHistoryWatch(dir, root)
	watch.claude = peer == "claude"
	return watch, nil
}

func (w *nativeHistoryWatch) saveIndexIfChanged() error {
	if w.indexSaved && !w.indexDirty {
		return nil
	}
	return w.saveIndex()
}
