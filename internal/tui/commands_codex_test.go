package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexSlashCommands_WithoutAuthority(t *testing.T) {
	_, ws, ctx := newControlWorkspace(t)
	out, err := ws.ExecuteCommand(ctx, "/codex")
	if err != nil {
		t.Fatalf("unexpected error executing /codex: %v", err)
	}
	if !strings.Contains(out, "Codex control authority unavailable") {
		t.Fatalf("expected unattached authority message, got: %q", out)
	}
}

func TestCodexSlashCommands_HelpDocumentation(t *testing.T) {
	_, ws, ctx := newControlWorkspace(t)
	help, err := ws.ExecuteCommand(ctx, "/help")
	if err != nil {
		t.Fatalf("/help returned error: %v", err)
	}
	if !strings.Contains(help, "/codex") {
		t.Fatalf("expected /codex to be documented in /help, got:\n%s", help)
	}
}

func TestCodexSlashCommands_WithAttachedAuthority(t *testing.T) {
	_, ws, ctx := newControlWorkspace(t)
	source, auth := testControl(t)
	ws.AttachControlSource(source)

	// 1. Status / Info
	for _, cmd := range []string{"/codex", "/codex status", "/codex info", "/codex health"} {
		out, err := ws.ExecuteCommand(ctx, cmd)
		if err != nil {
			t.Fatalf("%s returned error: %v", cmd, err)
		}
		for _, want := range []string{
			"CODEX GOVERNED CONTROL PLANE:",
			"Status:",
			"Available:",
			"App-Server:",
			"Active Model:",
			"/codex doctor",
			"/codex models",
		} {
			if !strings.Contains(out, want) {
				t.Fatalf("expected %q in %s output, got:\n%s", want, cmd, out)
			}
		}
	}

	// 2. Doctor diagnostics
	docOut, err := ws.ExecuteCommand(ctx, "/codex doctor")
	if err != nil {
		t.Fatalf("/codex doctor: %v", err)
	}
	if !strings.Contains(docOut, "CODEX DOCTOR DIAGNOSTICS") || !strings.Contains(docOut, "Overall Status:") {
		t.Fatalf("unexpected /codex doctor output:\n%s", docOut)
	}

	// 3. Models list
	modelsOut, err := ws.ExecuteCommand(ctx, "/codex models")
	if err != nil {
		t.Fatalf("/codex models: %v", err)
	}
	for _, want := range []string{"CODEX MODELS", "gpt-5.6-terra", "gpt-6-astra", "[SELECTED]"} {
		if !strings.Contains(modelsOut, want) {
			t.Fatalf("expected %q in /codex models output, got:\n%s", want, modelsOut)
		}
	}

	// 4. Model selection
	selectOut, err := ws.ExecuteCommand(ctx, "/codex model gpt-6-astra")
	if err != nil {
		t.Fatalf("/codex model: %v", err)
	}
	if !strings.Contains(selectOut, `Selected Codex model successfully switched to "gpt-6-astra"`) {
		t.Fatalf("unexpected selection output: %s", selectOut)
	}
	cur, _ := auth.SelectedCodexModel(ctx)
	if cur != "gpt-6-astra" {
		t.Fatalf("active model = %q, want gpt-6-astra", cur)
	}

	// 5. Query active model
	curOut, err := ws.ExecuteCommand(ctx, "/codex model")
	if err != nil {
		t.Fatalf("/codex model query: %v", err)
	}
	if !strings.Contains(curOut, "gpt-6-astra") {
		t.Fatalf("expected active model in query output, got: %s", curOut)
	}

	// 6. Refuse dangerous flag in model
	flagOut, err := ws.ExecuteCommand(ctx, "/codex model --dangerously-bypass-approvals-and-sandbox")
	if err != nil {
		t.Fatalf("model command failed unexpectedly: %v", err)
	}
	if !strings.Contains(flagOut, "Failed to select Codex model") {
		t.Fatalf("expected refusal for dangerous flag, got: %s", flagOut)
	}

	// 7. Commit review
	reviewOut, err := ws.ExecuteCommand(ctx, "/codex review")
	if err != nil {
		t.Fatalf("/codex review: %v", err)
	}
	if !strings.Contains(reviewOut, "CODEX COMMIT REVIEW") || !strings.Contains(reviewOut, "Verified:      true") {
		t.Fatalf("unexpected /codex review output:\n%s", reviewOut)
	}

	// 8. Sessions history
	sessOut, err := ws.ExecuteCommand(ctx, "/codex sessions")
	if err != nil {
		t.Fatalf("/codex sessions: %v", err)
	}
	if !strings.Contains(sessOut, "RECORDED CODEX SESSIONS") || !strings.Contains(sessOut, "session-codex-1") {
		t.Fatalf("unexpected /codex sessions output:\n%s", sessOut)
	}

	// 9. Plugins & Skills
	pluginsOut, err := ws.ExecuteCommand(ctx, "/codex plugins")
	if err != nil {
		t.Fatalf("/codex plugins: %v", err)
	}
	if !strings.Contains(pluginsOut, "CODEX PLUGINS") || !strings.Contains(pluginsOut, "Test Plugin") || !strings.Contains(pluginsOut, "test-skill") {
		t.Fatalf("unexpected /codex plugins output:\n%s", pluginsOut)
	}

	// 10. Skill install
	skillOut, err := ws.ExecuteCommand(ctx, "/codex skill install test-skill")
	if err != nil {
		t.Fatalf("/codex skill install: %v", err)
	}
	if !strings.Contains(skillOut, `Successfully installed project-local Codex skill "test-skill"`) ||
		!strings.Contains(skillOut, "Immutable Digest: sha256:test-skill") {
		t.Fatalf("unexpected skill install output: %s", skillOut)
	}

	// 11. Run/dispatch task
	runOut, err := ws.ExecuteCommand(ctx, "/codex run TASK-101")
	if err != nil {
		t.Fatalf("/codex run: %v", err)
	}
	if !strings.Contains(runOut, "Dispatched task TASK-101 to Codex") || !strings.Contains(runOut, "Run ID: RUN-CODEX-1") {
		t.Fatalf("unexpected /codex run output: %s", runOut)
	}

	// 12. Exec prompt
	execOut, err := ws.ExecuteCommand(ctx, "/codex exec analyze codebase architecture")
	if err != nil {
		t.Fatalf("/codex exec: %v", err)
	}
	for _, want := range []string{
		"CODEX TASK LAUNCHED:",
		"Task ID:   TASK-CODEX-",
		"Run ID:    RUN-CODEX-1",
		"Prompt:    analyze codebase architecture",
		"Model:     gpt-5.6-terra",
		"Status:    COMPLETED",
	} {
		if !strings.Contains(execOut, want) {
			t.Fatalf("expected %q in /codex exec output, got:\n%s", want, execOut)
		}
	}

	// 13. Unknown subcommand
	unknownOut, err := ws.ExecuteCommand(ctx, "/codex nonexistent")
	if err != nil {
		t.Fatalf("/codex nonexistent: %v", err)
	}
	if !strings.Contains(unknownOut, `Unknown Codex subcommand "nonexistent"`) {
		t.Fatalf("unexpected unknown subcommand response: %s", unknownOut)
	}

	// 14. Direct natural language at the composer, with no leading slash, must
	// start nothing at all: naming the agent is what consents to spending on it.
	directOut, err := ws.ExecuteCommand(ctx, "implement user authentication handler")
	if err != nil {
		t.Fatalf("direct prompt execution: %v", err)
	}
	if !strings.Contains(directOut, "Nothing was run") {
		t.Fatalf("plain text started something, got:\n%s", directOut)
	}
	if strings.Contains(directOut, "TASK LAUNCHED") {
		t.Fatalf("plain text launched a task, got:\n%s", directOut)
	}

	// 15. Multi-word prompt under /codex without explicit exec subcommand
	multiOut, err := ws.ExecuteCommand(ctx, "/codex refactor authentication module")
	if err != nil {
		t.Fatalf("/codex multi-word prompt: %v", err)
	}
	for _, want := range []string{"CODEX TASK LAUNCHED:", "refactor authentication module"} {
		if !strings.Contains(multiOut, want) {
			t.Fatalf("expected %q in multi-word prompt output, got:\n%s", want, multiOut)
		}
	}

	// 16. Single word prompt with /codex exec
	singleOut, err := ws.ExecuteCommand(ctx, "/codex exec test")
	if err != nil {
		t.Fatalf("/codex exec test: %v", err)
	}
	if !strings.Contains(singleOut, "CODEX TASK LAUNCHED:") || !strings.Contains(singleOut, "test") {
		t.Fatalf("unexpected /codex exec test output:\n%s", singleOut)
	}

	// 17. Sandbox inspect and switch
	sbOut, err := ws.ExecuteCommand(ctx, "/codex sandbox")
	if err != nil {
		t.Fatalf("/codex sandbox: %v", err)
	}
	if !strings.Contains(sbOut, "CODEX SANDBOX POLICY:") {
		t.Fatalf("unexpected /codex sandbox output: %s", sbOut)
	}
	sbSwitch, err := ws.ExecuteCommand(ctx, "/codex sandbox read-only")
	if err != nil {
		t.Fatalf("/codex sandbox read-only: %v", err)
	}
	if !strings.Contains(sbSwitch, "Policy unchanged") {
		t.Fatalf("unexpected sandbox switch output: %s", sbSwitch)
	}

	// 18. Approval inspect and switch
	appOut, err := ws.ExecuteCommand(ctx, "/codex approval")
	if err != nil {
		t.Fatalf("/codex approval: %v", err)
	}
	if !strings.Contains(appOut, "CODEX APPROVAL POLICY:") {
		t.Fatalf("unexpected /codex approval output: %s", appOut)
	}
	appSwitch, err := ws.ExecuteCommand(ctx, "/codex approval never")
	if err != nil {
		t.Fatalf("/codex approval never: %v", err)
	}
	if !strings.Contains(appSwitch, "Policy unchanged") {
		t.Fatalf("unexpected approval switch output: %s", appSwitch)
	}

	// 19. Search toggle
	searchOut, err := ws.ExecuteCommand(ctx, "/codex search")
	if err != nil {
		t.Fatalf("/codex search: %v", err)
	}
	if !strings.Contains(searchOut, "CODEX WEB SEARCH:") {
		t.Fatalf("unexpected /codex search output: %s", searchOut)
	}
	searchSwitch, err := ws.ExecuteCommand(ctx, "/codex search off")
	if err != nil {
		t.Fatalf("/codex search off: %v", err)
	}
	if !strings.Contains(searchSwitch, "Search unchanged") {
		t.Fatalf("unexpected search toggle output: %s", searchSwitch)
	}

	// 20. Agents and diff
	agentsOut, err := ws.ExecuteCommand(ctx, "/codex agents")
	if err != nil {
		t.Fatalf("/codex agents: %v", err)
	}
	if !strings.Contains(agentsOut, "CODEX APP-SERVER AGENTS & SESSIONS") {
		t.Fatalf("unexpected /codex agents output: %s", agentsOut)
	}
	diffOut, err := ws.ExecuteCommand(ctx, "/codex diff")
	if err != nil {
		t.Fatalf("/codex diff: %v", err)
	}
	if !strings.Contains(diffOut, "Diff viewer") {
		t.Fatalf("unexpected /codex diff output: %s", diffOut)
	}
}

func TestTopLevelCodexCommands_DirectRouting(t *testing.T) {
	// Never send an invented fixture task ID to the real Codex Cloud API.
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "codex"), []byte("#!/bin/sh\nif [ \"$1\" = apply ]; then echo 'fixture apply rejected'; exit 7; fi\necho 'fixture true'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	_, ws, ctx := newControlWorkspace(t)
	source, auth := testControl(t)
	ws.AttachControlSource(source)

	// 1. Direct /models
	modelsOut, err := ws.ExecuteCommand(ctx, "/models")
	if err != nil {
		t.Fatalf("/models: %v", err)
	}
	if !strings.Contains(modelsOut, "CODEX MODELS") {
		t.Fatalf("expected CODEX MODELS in /models output, got:\n%s", modelsOut)
	}

	// 2. Direct /model <slug>
	modelSwitch, err := ws.ExecuteCommand(ctx, "/model gpt-6-astra")
	if err != nil {
		t.Fatalf("/model gpt-6-astra: %v", err)
	}
	if !strings.Contains(modelSwitch, "gpt-6-astra") {
		t.Fatalf("unexpected switch output: %s", modelSwitch)
	}
	cur, _ := auth.SelectedCodexModel(ctx)
	if cur != "gpt-6-astra" {
		t.Fatalf("active model = %q, want gpt-6-astra", cur)
	}

	// 3. Direct /review
	revOut, err := ws.ExecuteCommand(ctx, "/review")
	if err != nil {
		t.Fatalf("/review: %v", err)
	}
	if !strings.Contains(revOut, "CODEX COMMIT REVIEW") {
		t.Fatalf("unexpected /review output: %s", revOut)
	}

	// 4. Direct /mcp
	mcpOut, err := ws.ExecuteCommand(ctx, "/mcp")
	if err != nil {
		t.Fatalf("/mcp: %v", err)
	}
	if !strings.Contains(mcpOut, "CODEX MCP SERVERS") {
		t.Fatalf("unexpected /mcp output: %s", mcpOut)
	}

	// 5. Direct /plugins
	plugOut, err := ws.ExecuteCommand(ctx, "/plugins")
	if err != nil {
		t.Fatalf("/plugins: %v", err)
	}
	if !strings.Contains(plugOut, "CODEX PLUGINS") {
		t.Fatalf("unexpected /plugins output: %s", plugOut)
	}

	// 6. Direct /apply
	applyOut, err := ws.ExecuteCommand(ctx, "/apply")
	if err != nil {
		t.Fatalf("/apply: %v", err)
	}
	if !strings.Contains(applyOut, "Codex apply failed") || strings.Contains(applyOut, "changes applied") {
		t.Fatalf("unexpected /apply output: %s", applyOut)
	}

	// 7. Direct /sessions
	sessOut, err := ws.ExecuteCommand(ctx, "/sessions")
	if err != nil {
		t.Fatalf("/sessions: %v", err)
	}
	if !strings.Contains(sessOut, "RECORDED CODEX SESSIONS") {
		t.Fatalf("unexpected /sessions output: %s", sessOut)
	}

	// 8. Direct /search
	searchOut, err := ws.ExecuteCommand(ctx, "/search")
	if err != nil {
		t.Fatalf("/search: %v", err)
	}
	if !strings.Contains(searchOut, "CODEX WEB SEARCH") {
		t.Fatalf("unexpected /search output: %s", searchOut)
	}

	// 9. Direct /features
	featOut, err := ws.ExecuteCommand(ctx, "/features")
	if err != nil {
		t.Fatalf("/features: %v", err)
	}
	if !strings.Contains(featOut, "CODEX FEATURE FLAGS") {
		t.Fatalf("unexpected /features output: %s", featOut)
	}

	// 10. Direct /sandbox with arg
	sbOut, err := ws.ExecuteCommand(ctx, "/sandbox workspace-write")
	if err != nil {
		t.Fatalf("/sandbox: %v", err)
	}
	if !strings.Contains(sbOut, "workspace-write") {
		t.Fatalf("unexpected /sandbox output: %s", sbOut)
	}
}

func TestDeveloperAgentCockpit_Rendering(t *testing.T) {
	_, ws, ctx := newControlWorkspace(t)
	_ = ws.RefreshState(ctx)

	// The workspace must not impersonate a connected native agent.
	prompt := ws.composer.PromptString()
	if strings.Contains(prompt, "@codex") {
		t.Fatalf("unexpected static agent label: %q", prompt)
	}

	// Frame rendering should show rich Developer Agent Cockpit when activity is empty
	frame := BuildFrame(ws.state, ws.theme, ws.workDir, ws.composer, nil, 100, 30)
	lines, _ := frame.Lines(100, 30)
	rendered := strings.Join(lines, "\n")

	for _, want := range []string{
		"Activity",
		"Native agent workspace",
		"[F1]", "[F2]", "[F3]", "[F4]", "[F5]", "[Esc]",
		"/codex new", "/codex continue", "/resume", "/diff", "[F7]",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected %q in initial screen rendering, got:\n%s", want, rendered)
		}
	}
}
