//go:build linux

package tui

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/testutil/testcloud"
)

// FindBinary also searches fixed system directories. Shadow installed fallbacks
// with failing doubles so these tests never execute an operator's provider CLI.
func sweepCrosscutEnvironment(t *testing.T) {
	t.Helper()
	sweepWorkEnvironment(t)
	for _, name := range []string{"codex", "claude", "opencode", "agy", "gemini"} {
		for _, directory := range []string{"/usr/local/bin", "/usr/bin"} {
			if info, err := os.Stat(filepath.Join(directory, name)); err == nil && !info.IsDir() {
				if err := os.WriteFile(filepath.Join(os.Getenv("PATH"), name), []byte("#!/bin/sh\nexit 127\n"), 0700); err != nil {
					t.Fatal(err)
				}
				break
			}
		}
	}
}

func TestCommandSweepCrosscutCatalog(t *testing.T) {
	// Read dispatch before the environment moves into an isolated project.
	source, err := os.ReadFile("commands.go")
	if err != nil {
		t.Fatal(err)
	}
	f, err := parser.ParseFile(token.NewFileSet(), "commands.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	dispatch := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		c, ok := n.(*ast.CaseClause)
		if !ok {
			return true
		}
		for _, e := range c.List {
			if lit, ok := e.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				s, _ := strconv.Unquote(lit.Value)
				if strings.HasPrefix(s, "/") {
					dispatch[s] = true
				}
			}
		}
		return true
	})
	sweepCrosscutEnvironment(t)
	installDialectDoubles(t)
	w := NewWorkspace(nil, "sweep", "sweep")
	help, err := w.cmd.Handle(context.Background(), "/help all")
	if err != nil {
		t.Fatal(err)
	}
	helpCommands := map[string]bool{}
	for _, c := range regexp.MustCompile(`(?:^|[ \t(])/[a-z?][a-z0-9?-]*`).FindAllString(help, -1) {
		helpCommands[strings.TrimLeft(c, " \t(")] = true
	}
	listed := map[string]bool{}
	for _, c := range w.completer.ctx.Commands {
		listed[c] = true
		if !dispatch[c] {
			t.Errorf("completion lacks dispatch: %s", c)
		}
		if !helpCommands[c] {
			t.Errorf("completion lacks help: %s", c)
		}
		// The update handler is called with a canceled context, before dialing.
		ctx := context.Background()
		if c == "/update" {
			var cancel context.CancelFunc
			ctx, cancel = context.WithCancel(ctx)
			cancel()
		}
		out, err := w.cmd.Handle(ctx, c)
		if strings.Contains(out, "Unknown command") || (out == "" && err == nil) {
			t.Errorf("root command is unhandled or silent: %s: %q", c, out)
		}
	}
	// Commands deliberately hidden from help and completion because their
	// subsystem is not available in this build; dispatch still answers them.
	hidden := map[string]bool{"/blind": true}
	for c := range dispatch {
		if hidden[c] {
			if out, _ := w.cmd.Handle(context.Background(), c); !strings.Contains(out, "not available") {
				t.Errorf("hidden command does not say it is unavailable: %s: %q", c, out)
			}
			continue
		}
		if !listed[c] {
			t.Errorf("dispatch lacks completion: %s", c)
		}
	}
	for c := range helpCommands {
		if !listed[c] {
			t.Errorf("help lacks completion: %s", c)
		}
	}
	for prefix, choices := range map[string][]string{
		"/marshal model": {"codex", "claude", "agy"}, "/marshal amend": {"approve", "deny"},
		"/marshal settings": {"execution-rights", "acceptance-mode", "rework-limit", "ultra-concurrency", "control"},
		"/memory peers":     {"participants", "claude", "codex", "opencode", "agy", "antigravity"},
	} {
		for _, c := range choices {
			found := false
			for _, v := range w.completer.ctx.Subcommands[prefix] {
				found = found || v == c
			}
			if !found {
				t.Errorf("missing completion %s %s", prefix, c)
			}
		}
	}
}

func TestCommandSweepCrosscutPeerValidation(t *testing.T) {
	sweepCrosscutEnvironment(t)
	w := NewWorkspace(nil, "sweep", "sweep")
	for _, line := range []string{"/memory peers codex none typo", "/memory peers participants none typo", "/memory peers codex none claude", "/memory peers codex ,", "/memory peers participants none"} {
		out, err := w.cmd.Handle(context.Background(), line)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(StripANSI(out), "SHARED CHANNEL") {
			t.Errorf("malformed input changed channel: %s: %s", line, out)
		}
	}
}

func TestCommandSweepCrosscutMarshalUnavailable(t *testing.T) {
	sweepCrosscutEnvironment(t)
	w := NewWorkspace(nil, "sweep", "sweep")
	out, err := w.cmd.Handle(context.Background(), "/marshal build a feature")
	if err == nil || !strings.Contains(err.Error(), "runtime") {
		t.Fatalf("missing runtime should refuse synchronously: %q %v", out, err)
	}
	if w.marshalPanel() != nil {
		t.Fatal("unavailable planning created a phantom run")
	}
}

func TestCommandSweepCrosscutQuitPTY(t *testing.T) {
	bin := buildMarshalBinary(t)
	sweepCrosscutEnvironment(t)
	project := initProject(t, bin)
	s := startFrozenTUIInProject(t, 40, 120, bin, project, "tui")
	s.sendLine("/QUIT extra")
	s.mustSee("Usage: /quit")
	s.sendLine("/QUIT")
	s.mustSee("Exiting MARSHAL")
	done := make(chan error, 1)
	go func() { done <- s.cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("accepted /QUIT did not exit")
	}
}

func TestCommandSweepCrosscutQuitScanner(t *testing.T) {
	sweepCrosscutEnvironment(t)
	w := NewWorkspace(nil, "sweep", "sweep")
	var out strings.Builder
	if err := w.runLineScanner(context.Background(), strings.NewReader("/QUIT\n/unknown\n"), &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "Unknown command") {
		t.Fatal("scanner continued after accepted quit")
	}
}

func TestCommandSweepCrosscutHandlers(t *testing.T) {
	sweepCrosscutEnvironment(t)
	_, stored, _ := newControlWorkspace(t)
	for _, w := range []*Workspace{stored, NewWorkspace(nil, "sweep", "sweep")} {
		w.workDir = t.TempDir()
		for _, line := range []string{"/help", "/?", "/help extra", "/? extra", "/quit extra", "/exit extra", "/memory", "/memory list", "/memory list extra", "/memory search", "/memory search missing term", "/memory provenance", "/memory provenance missing", "/memory provenance id extra", "/memory typo", "/memory inject", "/memory inject preview", "/memory inject preview agy", "/memory inject preview antigravity", "/memory inject preview typo", "/memory inject preview codex extra", "/memory inject clear", "/memory inject clear extra", "/memory inject typo", "/memory peers", "/memory peers codex", "/memory peers typo all", "/memory peers codex typo", "/ultra", "/ultra status", "/ultra start", "/ultra stop", "/ultra stop confirm", "/ultra request", "/ultra typo", "/ultra status extra", "/ultra start extra", "/ultra stop extra", "/ultra stop confirm extra", "/ultra request extra", "/marshal", "/marshal help", "/marshal status", "/marshal chat", "/marshal approve", "/marshal use-plan", "/marshal approve-task", "/marshal approve-task missing", "/marshal approve-task id extra", "/marshal close", "/marshal accept", "/marshal accept missing", "/marshal accept id extra", "/marshal return", "/marshal return task reason", "/marshal resume", "/marshal stop", "/marshal model", "/marshal model codex", "/marshal model claude", "/marshal model agy", "/marshal model typo", "/marshal model codex extra", "/marshal settings", "/marshal settings typo value", "/marshal settings control", "/marshal settings control free extra", "/marshal amend", "/marshal amend reason", "/marshal amend approve", "/marshal amend deny", "/marshal stauts", "plain prompt", "help", "/unknown"} {
			t.Run(strings.ReplaceAll(line, "/", "_")+"-"+w.sessionID, func(t *testing.T) {
				out, err := w.cmd.Handle(context.Background(), line)
				if out == "" && err == nil {
					t.Fatalf("silent command: %s", line)
				}
			})
		}
		// Exercise every advertised subcommand's spelling and malformed arguments.
		for prefix, subs := range w.completer.ctx.Subcommands {
			if !(prefix == "/memory" || strings.HasPrefix(prefix, "/memory ") || prefix == "/ultra" || strings.HasPrefix(prefix, "/ultra ") || prefix == "/marshal" || strings.HasPrefix(prefix, "/marshal ")) {
				continue
			}
			for _, sub := range subs {
				for _, suffix := range []string{"", " value", " value extra --unknown"} {
					line := prefix + " " + sub + suffix
					out, err := w.cmd.Handle(context.Background(), line)
					if out == "" && err == nil {
						t.Errorf("silent advertised subcommand: %s", line)
					}
				}
				line := prefix + " " + sub + "typo"
				out, err := w.cmd.Handle(context.Background(), line)
				if out == "" && err == nil {
					t.Errorf("silent misspelled subcommand: %s", line)
				}
			}
		}
		for _, sub := range []string{"help", "status", "chat", "approve", "use-plan", "close", "stop", "resume"} {
			_, err := w.cmd.Handle(context.Background(), "/marshal "+sub+" extra")
			if err == nil {
				t.Errorf("extra argument accepted for %s", sub)
			}
		}
		for _, channel := range []string{"auto", "system-prompt", "project-doc", "prompt", "off"} {
			sweepWorkRun(t, w, "/memory inject "+channel, "set to")
			sweepWorkRun(t, w, "/memory inject "+channel+" extra", "Usage:")
		}
		for _, reader := range append([]string{"participants", "agy"}, knownProviders...) {
			for _, authors := range []string{"all", "none", "claude,codex", "opencode agy"} {
				if reader == "participants" && authors == "none" {
					continue
				}
				sweepWorkRun(t, w, "/memory peers "+reader+" "+authors, "SHARED CHANNEL")
			}
		}
	}
}

func TestCommandSweepCrosscutPTY(t *testing.T) {
	bin := buildMarshalBinary(t)
	sweepCrosscutEnvironment(t)
	project := initProject(t, bin)
	useCodexByDefault(t, project)
	t.Setenv("HTTPS_PROXY", "http://[invalid")
	s := startFrozenTUIInProject(t, 50, 140, bin, project, "tui")
	for _, c := range []struct{ line, want string }{{"/help all", "MARSHAL Terminal Workspace Commands:"}, {"/memory peers", "SHARED CHANNEL"}, {"/ultra status", "ULTRA status: INACTIVE"}, {"/marshal status", "No Marshal"}, {"plain prompt", "Nothing was run"}, {"/unknown", "Unknown command"}} {
		s.sendLine(c.line)
		s.mustSee(c.want)
		if c.line == "/help all" {
			s.send("\x1b[4~") // End reaches the retained keyboard reference.
			s.mustSee("Function Keys & Shortcuts")
		}
	}
	// Popup keys preserve the draft; Enter accepts, then submits.
	s.send("/memor")
	draft := s.composerLine(50, 140)
	s.send("\x1b[B\x1b[A")
	if s.composerLine(50, 140) != draft {
		t.Fatal("arrows changed completion draft")
	}
	s.send("\t\x1b[Z\x1b")
	if !strings.Contains(s.composerLine(50, 140), "/memory") {
		t.Fatal("completion lost command")
	}
	s.send("\x15")
	s.send("\x0e")
	s.mustSee("not available yet")
	s.send("\x1b")
	s.mustSee("not available yet")
	// Give F2 an uncommitted target so its missing-provider path is exercised.
	if err := os.WriteFile(filepath.Join(project, "review-change.txt"), []byte("uncommitted"), 0600); err != nil {
		t.Fatal(err)
	}
	// A malformed proxy URL refuses HTTP before any network connection.
	for _, key := range []struct{ raw, want string }{
		{"\x1bOP", "Talk to the Marshal: /marshal chat"}, {"\x1bOQ", "Install Codex"},
		{"\x1bOR", "Git Diff"}, {"\x1bOS", "CANONICAL STATUS DETAIL"},
		{"\x1b[15~", "Discover models failed"}, {"\x1b[17~", "Install Codex"},
		{"\x1b[18~", "Install Codex"}, {"\x1b[19~", "Install Claude"},
		{"\x1b[20~", "OpenCode exited"}, {"\x1b[21~", "Update check failed"},
		{"\x1b[24~", "Antigravity exited"},
	} {
		if key.raw == "\x1b[20~" {
			if _, err := os.Stat(filepath.Join(os.Getenv("PATH"), "opencode")); os.IsNotExist(err) {
				key.want = "Install OpenCode"
			}
		}
		if key.raw == "\x1b[24~" {
			if _, err := os.Stat(filepath.Join(os.Getenv("PATH"), "agy")); os.IsNotExist(err) {
				key.want = "Install Antigravity"
			}
		}
		before := len(s.output())
		s.send(key.raw)
		s.mustSee(key.want)
		if len(s.output()) == before || !strings.Contains(s.screenText(50, 140), key.want) {
			t.Fatalf("key %q produced no new %q output", key.raw, key.want)
		}
		if key.raw == "\x1bOR" {
			s.send("\x1b")
		}
	}
	s.send("draft")
	draft = s.composerLine(50, 140)
	s.send("\x1b[23~") // F11 is explicitly unassigned.
	if s.composerLine(50, 140) != draft {
		t.Fatal("F11 changed the draft")
	}
	s.send("\x15")

	s.sendLine("/quit")
}

func TestCommandSweepCrosscutCaseInsensitiveCompletion(t *testing.T) {
	sweepCrosscutEnvironment(t)
	w := NewWorkspace(nil, "sweep", "sweep")
	w.composer.SetText("/QUIT")
	w.refreshCompletion()
	if w.completionOpen {
		t.Fatal("complete accepted command is swallowed by popup")
	}
}

func TestCommandSweepCrosscutFunctionKeyParsing(t *testing.T) {
	for i, raw := range []string{"\x1bOP", "\x1bOQ", "\x1bOR", "\x1bOS", "\x1b[15~", "\x1b[17~", "\x1b[18~", "\x1b[19~", "\x1b[20~", "\x1b[21~", "\x1b[23~", "\x1b[24~"} {
		if got := ParseKey([]byte(raw)); got.Type != KeyF1+KeyType(i) {
			t.Errorf("F%d decoded as %v", i+1, got.Type)
		}
	}
}

func TestCommandSweepCrosscutMarshalSettings(t *testing.T) {
	sweepCrosscutEnvironment(t)
	_, w, ctx := acceptanceWorkspace(t)
	for key, values := range map[string][]string{"execution-rights": {"none", "read-only", "small-tasks"}, "acceptance-mode": {"marshal", "marshal-then-user", "user"}, "control": {"free", "strict"}, "rework-limit": {"0", "3"}, "ultra-concurrency": {"1", "4"}} {
		for _, value := range values {
			out, err := w.cmd.Handle(ctx, "/marshal settings "+key+" "+value)
			if err != nil || !strings.Contains(out, "set to "+value) {
				t.Fatalf("%s %s: %q %v", key, value, out, err)
			}
			out, err = w.cmd.Handle(ctx, "/marshal settings")
			if err != nil || !strings.Contains(out, key+" "+value) {
				t.Fatalf("readback %q %v", out, err)
			}
		}
		for _, suffix := range []string{"", " typo", " valid extra"} {
			if _, err := w.cmd.Handle(ctx, "/marshal settings "+key+suffix); err == nil {
				t.Errorf("accepted invalid %s%s", key, suffix)
			}
		}
	}
	for _, line := range []string{"/marshal settings unknown value", "/marshal settings rework-limit -1", "/marshal settings ultra-concurrency 0"} {
		if _, err := w.cmd.Handle(ctx, line); err == nil {
			t.Errorf("accepted %s", line)
		}
	}
	for _, line := range []string{"/marshal approve", "/marshal resume", "/marshal close", "/marshal return task reason", "/marshal accept task", "/marshal approve-task id", "/marshal use-plan", "/marshal chat"} {
		if out, err := w.cmd.Handle(ctx, line); err == nil {
			t.Errorf("no run/plan/terminal should refuse %s: %s", line, out)
		}
	}
	// Explicit model switches require an installed CLI. Provider configuration
	// may still name an absent CLI; asynchronous planning must fail visibly.
	if _, err := w.cmd.Handle(ctx, "/marshal model codex"); err == nil || !strings.Contains(err.Error(), "CLI is missing") {
		t.Fatalf("missing CLI model switch: %v", err)
	}
	if _, err := w.cmd.Handle(ctx, "/provider use codex"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.cmd.Handle(ctx, "/marshal build a greeting"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if p := w.marshalPanel(); p != nil && strings.Contains(p.Note, "planning failed") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("missing provider did not produce planning failure")
}

func TestCommandSweepCrosscutMemoryOverview(t *testing.T) {
	sweepCrosscutEnvironment(t)
	w := NewWorkspace(nil, "sweep", "sweep")
	out, err := w.cmd.Handle(context.Background(), "/memory")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"candidate", "/memory list", "/memory provenance", "/memory peers"} {
		if !strings.Contains(out, want) {
			t.Errorf("memory overview missing %s", want)
		}
	}
}

func TestCommandSweepCrosscutUltraTransitions(t *testing.T) {
	sweepCrosscutEnvironment(t)
	w := NewWorkspace(nil, "sweep", "sweep")
	for _, delegates := range []bool{true, false} {
		capabilities := []string{}
		if delegates {
			capabilities = nil
		}
		w.AttachULTRA(testcloud.EntitledGate(t, testcloud.Options{Capabilities: capabilities}), false)
		sweepWorkRun(t, w, "/ultra status", "ENTITLED, EXECUTION OFF")
		sweepWorkRun(t, w, "/ultra start", "Execution is on")
		sweepWorkRun(t, w, "/ultra status", "ACTIVE")
		sweepWorkRun(t, w, "/ultra stop", "Do you really want")
		sweepWorkRun(t, w, "/ultra stop extra", "Usage:")
		sweepWorkRun(t, w, "/ultra stop confirm", "Do you really want")
		sweepWorkRun(t, w, "/ultra stop confirm", "Execution is off")
		sweepWorkRun(t, w, "/ultra stop", "already off")
		sweepWorkRun(t, w, "/ultra start", "Execution is on")
		sweepWorkRun(t, w, "/ultra stop", "Do you really want")
		sweepWorkRun(t, w, "plain text", "Nothing was run")
		sweepWorkRun(t, w, "/ultra stop confirm", "Do you really want")
		sweepWorkRun(t, w, "/ultra stop confirm", "Execution is off")
	}
	w.AttachULTRA(nil, false)
	w.AttachULTRAError(errors.New("test activation failure"))
	sweepWorkRun(t, w, "/ultra status", "activation failed")
	sweepWorkRun(t, w, "/ultra start", "not started")
	sweepWorkRun(t, w, "/ultra request", "not configured")
}

func TestCommandSweepCrosscutMemoryRecords(t *testing.T) {
	sweepCrosscutEnvironment(t)
	st, w, ctx := newMutationWorkspace(t)
	w.workDir = t.TempDir()
	now := time.Now().UTC()
	r := model.MemoryRecordV2{ID: "MEM-sweep4", ProjectID: w.projectID, Kind: model.MemoryKindDecision, Lifecycle: model.MemoryDurable, Authority: model.AuthorityVerified, Title: "Sweep greeting", Body: "Greeting from agent", Scope: string(model.ScopeSession), ScopeID: w.sessionID, SessionID: w.sessionID, ObservedAt: now, ValidFrom: now, CreatedAt: now, UpdatedAt: now, Source: model.MemorySource{Kind: "runtime", Reference: w.sessionID}, ExtMeta: map[string]any{"provider": "codex"}}
	if err := st.WriteMemoryV2(ctx, r); err != nil {
		t.Fatal(err)
	}
	sweepWorkRun(t, w, "/memory list", "MEM-sweep4")
	sweepWorkRun(t, w, "/memory search greeting", "MEM-sweep4")
	sweepWorkRun(t, w, "/memory search unmatched", "No memory records match")
	sweepWorkRun(t, w, "/memory provenance MEM-sweep4", "Authority:")
	sweepWorkRun(t, w, "/memory provenance missing", "No memory record")
	for _, provider := range []string{"claude", "opencode", "agy", "antigravity"} {
		sweepWorkRun(t, w, "/memory inject preview "+provider, "Greeting from agent")
	}
}

func TestCommandSweepCrosscutF3ErrorPTY(t *testing.T) {
	bin := buildMarshalBinary(t)
	sweepCrosscutEnvironment(t)
	project := initProject(t, bin)
	s := startFrozenTUIInProject(t, 40, 120, bin, project, "tui")
	// Corrupt the real Git index after startup so the trusted inspection fails.
	if err := os.WriteFile(filepath.Join(project, ".git", "index"), []byte("invalid index"), 0600); err != nil {
		t.Fatal(err)
	}
	s.send("\x1bOR")
	s.mustSee("Diff error:")
	s.mustSee("retry /diff")
}

func TestCommandSweepCrosscutMemoryProvenanceBeyondListLimit(t *testing.T) {
	sweepCrosscutEnvironment(t)
	st, w, ctx := newMutationWorkspace(t)
	w.workDir = t.TempDir()
	now := time.Now().UTC()
	r := model.MemoryRecordV2{ProjectID: w.projectID, Kind: model.MemoryKindDecision, Lifecycle: model.MemoryDurable, Authority: model.AuthorityVerified, Title: "Sweep record", Body: "Recorded decision", Scope: string(model.ScopeSession), ScopeID: w.sessionID, SessionID: w.sessionID, ObservedAt: now, ValidFrom: now, Source: model.MemorySource{Kind: "runtime", Reference: w.sessionID}}
	for i := 0; i < 501; i++ {
		r.ID = "MEM-sweep-" + strconv.Itoa(i)
		r.CreatedAt = now.Add(time.Duration(i) * time.Second)
		r.UpdatedAt = r.CreatedAt
		if err := st.WriteMemoryV2(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	sweepWorkRun(t, w, "/memory provenance MEM-sweep-0", "MEMORY MEM-sweep-0")
	// List/search currently inspect only the newest 200 rows (DECISION D1).
	sweepWorkRun(t, w, "/memory search Recorded decision", "200 of 200")
	sweepWorkRun(t, w, "/memory list", "200 of 200")
	// A globally valid ID must still be refused in another project.
	w.state.ProjectID = "other-project"
	sweepWorkRun(t, w, "/memory provenance MEM-sweep-0", "No memory record")
}

func TestCommandSweepCrosscutMarshalRecoveryHints(t *testing.T) {
	sweepCrosscutEnvironment(t)
	w := NewWorkspace(nil, "sweep", "sweep")
	for _, line := range []string{"/marshal settings control", "/marshal settings control free extra"} {
		_, err := w.cmd.Handle(context.Background(), line)
		if err == nil || !strings.Contains(err.Error(), "usage:") {
			t.Errorf("malformed input needs usage: %s: %v", line, err)
		}
	}
	for _, line := range []string{"/marshal settings", "/marshal chat"} {
		_, err := w.cmd.Handle(context.Background(), line)
		if err == nil || !strings.Contains(err.Error(), "reopen") {
			t.Errorf("missing runtime needs recovery hint: %s: %v", line, err)
		}
	}
	_, err := w.cmd.Handle(context.Background(), "/marshal accept task")
	if err == nil || !strings.Contains(err.Error(), "/marshal chat") {
		t.Errorf("missing run needs recovery hint: %v", err)
	}
}

func TestCommandSweepCrosscutMemoryClear(t *testing.T) {
	sweepCrosscutEnvironment(t)
	w := NewWorkspace(nil, "sweep", "sweep")
	for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
		data := "Operator instructions\n\n" + projectDocStartMarker + "\nTest briefing\n" + projectDocEndMarker + "\n"
		if err := os.WriteFile(filepath.Join(w.workDir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	sweepWorkRun(t, w, "/memory inject clear extra", "Usage:")
	for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
		data, err := os.ReadFile(filepath.Join(w.workDir, name))
		if err != nil || !strings.Contains(string(data), "Test briefing") {
			t.Fatal("malformed clear removed briefing")
		}
	}
	sweepWorkRun(t, w, "/memory inject clear", "2 project document(s)")
	for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
		data, err := os.ReadFile(filepath.Join(w.workDir, name))
		if err != nil || strings.Contains(string(data), "Test briefing") || !strings.Contains(string(data), "Operator instructions") {
			t.Fatalf("clear damaged %s: %q %v", name, data, err)
		}
	}
	sweepWorkRun(t, w, "/memory inject clear", "No MARSHAL memory block")
}

func TestCommandSweepCrosscutDecisions(t *testing.T) {
	sweepCrosscutEnvironment(t)
	_, w, ctx := acceptanceWorkspace(t)
	m := w.marshalSession()
	m.runID = "RUN-sweep4"
	w.state.Marshal = &MarshalPanel{RunID: m.runID, Tasks: []MarshalTaskRow{{ID: "pending"}}}
	m.service = w.runtime.Marshal()
	run := marshal.Run{PlanID: "PLAN-sweep4", PlanVersion: 1, State: marshal.AwaitingUser, Settings: marshal.DefaultSettings(), Tasks: []marshal.Task{{PlanTaskID: "pending", State: marshal.Queued}}}
	if _, err := m.service.Store.SetMarshalRun(ctx, w.projectID, m.runID, run, 0); err != nil {
		t.Fatal(err)
	}
	// Consent requires an existing current result; queued work has no authority.
	if _, err := w.cmd.Handle(ctx, "/marshal accept pending"); err == nil {
		t.Fatal("queued task accepted")
	}
	if len(m.approvals) != 0 {
		t.Fatal("queued task stored consent")
	}
	run.Tasks[0].State, run.Tasks[0].ResultCommit = marshal.HandedIn, "result"
	if _, err := m.service.Store.SetMarshalRun(ctx, w.projectID, m.runID, run, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := m.service.Store.SetMarshalTask(ctx, m.runID, run.Tasks[0], 0); err != nil {
		t.Fatal(err)
	}
	if _, err := m.service.Store.SetMarshalHandIn(ctx, m.runID, "pending", 1, marshal.HandIn{ResultCommit: "result"}); err != nil {
		t.Fatal(err)
	}
	purpose, err := m.service.TaskAcceptance(ctx, m.runID, "pending")
	if err != nil {
		t.Fatal(err)
	}
	sweepWorkRun(t, w, "/marshal accept pending", purpose)
	if _, err := m.approver(ctx, m.runID, purpose); err != nil {
		t.Fatal(err)
	}
	if _, err := m.approver(ctx, m.runID, purpose); err == nil {
		t.Fatal("reused task approval")
	}
	m.amended = true
	m.pending = &marshalAmendment{reason: "draft"}
	sweepWorkRun(t, w, "/marshal amend deny", "Amendment denied")
	if m.pending != nil || m.amended {
		t.Fatal("denial kept pending amendment")
	}
	if _, err := w.cmd.Handle(ctx, "/marshal amend approve"); err == nil {
		t.Fatal("approved absent amendment")
	}
	// The spelling guard applies to a single word; additional words are a goal.
	if out, err := w.cmd.Handle(ctx, "/marshal setings please"); err != nil || !strings.Contains(out, "drafting") {
		t.Fatalf("goal/verb ambiguity changed: %q %v", out, err)
	}
	w.marshalStop()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		busy := m.busy
		m.mu.Unlock()
		if !busy {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("planning did not stop")
}

func TestCommandSweepCrosscutMarshalApprovedPlanPTY(t *testing.T) {
	// Build before Chdir; the existing approval harness then reuses the binary.
	_ = buildMarshalBinary(t)
	sweepCrosscutEnvironment(t)
	script := "#!/bin/sh\nif [ \"$1\" = --version ]; then printf 'codex-cli sweep-test\\n'; exit 0; fi\nexit 127\n"
	if err := os.WriteFile(filepath.Join(os.Getenv("PATH"), "codex"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	// The shared PTY helper creates and approves a Process 04 plan, drives
	// /marshal use-plan to its Process 05 pause, and approves the bound task.
	s := startApprovedProcess05PTY(t, "gpt-test", "claude")
	s.sendLine("/marshal approve-task unrelated-id")
	s.mustSee("does not belong to the active Marshal plan")
	s.sendLine("/marshal close")
	s.mustSee("not verified")
	s.sendLine("/marshal status")
	if !strings.Contains(s.screenText(40, 160), "fix") {
		t.Fatal("failed close erased the task from Marshal status")
	}
	s.sendLine("/marshal stop")
	s.mustSee("No Marshal operation is running")
}

func TestCommandSweepCrosscutMarshalFailuresKeepPanel(t *testing.T) {
	sweepCrosscutEnvironment(t)
	_, w, ctx := acceptanceWorkspace(t)
	m := w.marshalSession()
	m.runID = "RUN-sweep-panel"
	m.service = w.runtime.Marshal()
	for _, line := range []string{"/marshal close", "/marshal amend add a task", "/marshal approve", "/marshal resume"} {
		w.state.Marshal = &MarshalPanel{RunID: m.runID, Tasks: []MarshalTaskRow{{ID: "original-task"}}}
		if _, err := w.cmd.Handle(ctx, line); err == nil || !strings.Contains(err.Error(), "No Marshal run yet; start one with /marshal chat") {
			t.Fatalf("%s must refuse the unsaved run immediately: %v", line, err)
		}
		p := w.marshalPanel()
		if p == nil || len(p.Tasks) != 1 || p.Tasks[0].ID != "original-task" {
			t.Fatalf("%s erased the prior task snapshot: %+v", line, p)
		}
		m.mu.Lock()
		busy := m.busy
		m.mu.Unlock()
		if busy {
			t.Fatalf("%s started work on an unsaved run", line)
		}
	}
}

func TestCommandSweepCrosscutQuitWithoutDurableSession(t *testing.T) {
	sweepCrosscutEnvironment(t)
	w := NewWorkspace(nil, "sweep", "")
	for _, line := range []string{"/quit", "/exit"} {
		out, err := w.cmd.Handle(context.Background(), line)
		if err != nil || strings.Contains(out, "Session remains durable") || !strings.Contains(out, "preserved") {
			t.Fatalf("exit claimed unbacked durability: %q %v", out, err)
		}
	}
	var out strings.Builder
	if err := w.runLineScanner(context.Background(), strings.NewReader("/quit\n"), &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "Session remains durable") {
		t.Fatal("scanner claimed unbacked durability")
	}
}
