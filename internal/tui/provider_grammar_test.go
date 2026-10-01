//go:build linux

package tui

import (
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

func TestProviderGrammarRejected(t *testing.T) {
	sweepWorkEnvironment(t)
	st, ws, ctx := acceptanceWorkspace(t)
	logPath := sweepAgentCodexDouble(t)
	before := map[string]int{}
	for _, table := range []string{"tasks", "sessions", "agents", "leases"} {
		count, err := st.Count(ctx, table)
		if err != nil {
			t.Fatal(err)
		}
		before[table] = count
	}
	source, auth := testControl(t)
	ws.AttachControlSource(source)
	for _, root := range []string{"/codex", "/claude", "/opencode", "/agy", "/antigravity"} {
		for _, tail := range []string{"modle slug", "zzzzzz slug"} {
			out := sweepAgentExecute(t, ws, root+" "+tail)
			if !strings.Contains(out, "To send a prompt") {
				t.Errorf("%s %s: %s", root, tail, out)
			}
		}
	}
	out := sweepAgentExecute(t, ws, "/agy fork session")
	if !strings.Contains(out, "To send a prompt") {
		t.Errorf("fork: %s", out)
	}
	for table, count := range before {
		after, err := st.Count(ctx, table)
		if err != nil || after != count {
			t.Fatalf("%s before=%d after=%d err=%v", table, count, after, err)
		}
	}
	for _, adapter := range []string{"codex", "claude", "opencode", "antigravity"} {
		runs, err := st.WorkerRunsByAdapter(ctx, adapter, 100)
		if err != nil || len(runs) != 0 {
			t.Fatalf("%s runs=%v err=%v", adapter, runs, err)
		}
	}
	if data, err := os.ReadFile(logPath); !os.IsNotExist(err) {
		t.Fatalf("provider invoked: %q (%v)", data, err)
	}
	if atomic.LoadInt32(&auth.codexDispatches) != 0 || atomic.LoadInt32(&auth.claudeDispatches) != 0 {
		t.Fatal("rejected input dispatched work")
	}
}

func TestProviderGrammarExplicitGovernedPrompts(t *testing.T) {
	sweepWorkEnvironment(t)
	_, ws, _ := newControlWorkspace(t)
	source, auth := testControl(t)
	ws.AttachControlSource(source)
	for _, root := range []string{"/codex", "/claude"} {
		for _, tail := range []string{`"fix the bug"`, `'model slug'`, "exec fix the bug", "EXEC fix the bug"} {
			out := sweepAgentExecute(t, ws, root+" "+tail)
			if !strings.Contains(out, "TASK LAUNCHED") {
				t.Fatalf("%s %s: %s", root, tail, out)
			}
		}
	}
	if auth.codexDispatches != 4 || auth.claudeDispatches != 4 {
		t.Fatalf("dispatches codex=%d claude=%d", auth.codexDispatches, auth.claudeDispatches)
	}
}

func TestProviderGrammarParser(t *testing.T) {
	for _, provider := range []string{"codex", "claude", "opencode", "agy", "antigravity"} {
		root := "/" + provider
		for _, tc := range []struct{ tail, prompt, rejection string }{
			{"", "", ""}, {"cli modle --flag", "", ""},
			{`"model slug"`, "model slug", ""}, {`'fix the bug' extra`, "fix the bug extra", ""},
			{"zzzzzz slug", "", "Unknown subcommand."},
			{"modle slug", "", "Nothing was run. Did you mean"},
			{"moel slug", "", "Nothing was run. Did you mean"},
			{`""`, "", "Nothing was run."},
		} {
			line := root + " " + tc.tail
			args, err := nativeArgs(tc.tail)
			if err != nil {
				t.Fatal(err)
			}
			prompt, rejection := parseProviderCommand(provider, line, args)
			if prompt != tc.prompt || (tc.rejection == "" && rejection != "") || (tc.rejection != "" && !strings.HasPrefix(rejection, tc.rejection)) {
				t.Errorf("%s: prompt=%q rejection=%q", line, prompt, rejection)
			}
		}
		key := provider
		if key == "antigravity" {
			key = "agy"
		}
		ws := NewWorkspace(nil, "grammar", "grammar")
		for _, op := range ws.completer.ctx.Subcommands[root] {
			if !oneOf(op, providerSubcommands[key]...) {
				t.Errorf("%s completion is outside wrapper grammar: %s", root, op)
			}
		}
	}
}

func TestProviderGrammarManagementAliasQuotedValue(t *testing.T) {
	for _, tc := range []struct{ provider, line, sub string }{
		{"codex", `/mcp "server name"`, "mcp"},
		{"codex", `/review "review instructions"`, "review"},
		{"codex", `/resume "session id"`, "resume"},
	} {
		prompt, rejection := parseProviderCommand(tc.provider, tc.line, []string{tc.sub, "quoted value"})
		if prompt != "" || rejection != "" {
			t.Fatalf("management alias became prompt: %s %q %q", tc.line, prompt, rejection)
		}
	}
}
