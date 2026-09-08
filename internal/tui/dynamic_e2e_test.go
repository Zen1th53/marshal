package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

// TestInputKeyInteractions tests all required keyboard contracts:
// ↑/↓ history, ←/→ cursor, Tab/Shift+Tab completion, @mentions, #claims,
// Ctrl+P palette, Ctrl+R reverse search, Ctrl+W word delete, and Enter.
func TestInputKeyInteractions(t *testing.T) {
	th := NewTheme(ThemeDefault, true, true)
	c := NewComposer(th)

	// 1. History addition and navigation (↑ / ↓)
	c.AddHistory("/status")
	c.AddHistory("/goal update")
	c.AddHistory("/route")

	c.HandleKey(KeyEvent{Type: KeyUp})
	if c.Text() != "/route" {
		t.Fatalf("expected /route from Up arrow, got %q", c.Text())
	}
	c.HandleKey(KeyEvent{Type: KeyUp})
	if c.Text() != "/goal update" {
		t.Fatalf("expected /goal update from Up arrow, got %q", c.Text())
	}
	c.HandleKey(KeyEvent{Type: KeyDown})
	if c.Text() != "/route" {
		t.Fatalf("expected /route from Down arrow, got %q", c.Text())
	}
	c.HandleKey(KeyEvent{Type: KeyDown})
	if c.Text() != "" {
		t.Fatalf("expected empty buffer after Down past history, got %q", c.Text())
	}

	// 2. Cursor navigation (← / →, Home / End, Ctrl+A / Ctrl+E)
	for _, r := range "go test -v" {
		c.HandleKey(KeyEvent{Type: KeyRune, Rune: r})
	}
	c.HandleKey(KeyEvent{Type: KeyHome})
	if c.CursorPos() != 0 {
		t.Fatalf("expected cursor at 0 on Home, got %d", c.CursorPos())
	}
	c.HandleKey(KeyEvent{Type: KeyRight})
	c.HandleKey(KeyEvent{Type: KeyRight})
	if c.CursorPos() != 2 {
		t.Fatalf("expected cursor at 2, got %d", c.CursorPos())
	}
	// Middle insertion
	for _, r := range "ing" {
		c.HandleKey(KeyEvent{Type: KeyRune, Rune: r})
	}
	if c.Text() != "going test -v" {
		t.Fatalf("expected middle insertion 'going test -v', got %q", c.Text())
	}
	c.HandleKey(KeyEvent{Type: KeyEnd})
	if c.CursorPos() != len(c.Text()) {
		t.Fatalf("expected cursor at end, got %d", c.CursorPos())
	}

	// 3. Word delete (Ctrl+W)
	c.HandleKey(KeyEvent{Type: KeyCtrlW})
	if c.Text() != "going test " {
		t.Fatalf("expected 'going test ', got %q", c.Text())
	}

	// 4. Autocomplete (Tab / Shift+Tab)
	compCtx := CompletionContext{
		Commands: []string{"/status", "/rollback", "/route", "/claims", "/inspect"},
		Agents:   []string{"claude", "codex", "opencode"},
		Claims:   []string{"C-10", "C-21"},
	}
	comp := NewCompleter(compCtx)

	// Command Tab completion
	newText, _, ok := comp.Complete("/ro", 3, false)
	if !ok || newText != "/rollback" {
		t.Fatalf("expected /rollback, got %q", newText)
	}
	// Shift+Tab cycles reverse
	newText, _, ok = comp.Complete(newText, len(newText), true)
	if !ok || newText != "/route" {
		t.Fatalf("expected /route on reverse tab, got %q", newText)
	}

	// @mention completion
	comp.Reset()
	newText, _, ok = comp.Complete("@co", 3, false)
	if !ok || newText != "@codex " {
		t.Fatalf("expected @codex , got %q", newText)
	}

	// Object claim completion
	comp.Reset()
	newText, _, ok = comp.Complete("/inspect C-2", 12, false)
	if !ok || newText != "/inspect C-21 " {
		t.Fatalf("expected /inspect C-21 , got %q", newText)
	}

	// 5. Command Palette (Ctrl+P)
	actions := GlobalRegistry.ToPaletteActions()
	pal := NewCommandPalette(th, actions)
	pal.Open()
	if !pal.IsOpen() {
		t.Fatalf("expected palette open")
	}
	for _, r := range "doctor" {
		pal.HandleKey(KeyEvent{Type: KeyRune, Rune: r})
	}
	selected, ok := pal.HandleKey(KeyEvent{Type: KeyEnter})
	if !ok || selected == nil || selected.Command != "/doctor" {
		t.Fatalf("expected /doctor selected from palette, got ok=%v, act=%+v", ok, selected)
	}
	if pal.IsOpen() {
		t.Fatalf("expected palette closed after enter")
	}

	// 6. Ctrl+R Reverse Search
	c.SetText("")
	c.AddHistory("/doctor --full")
	c.AddHistory("/checkpoint create alpha")
	c.HandleKey(KeyEvent{Type: KeyCtrlR})
	for _, r := range "check" {
		c.HandleKey(KeyEvent{Type: KeyRune, Rune: r})
	}
	if c.Text() != "/checkpoint create alpha" {
		t.Fatalf("expected matched /checkpoint create alpha, got %q", c.Text())
	}
	c.HandleKey(KeyEvent{Type: KeyEnter})
	if c.IsSearchMode() {
		t.Fatalf("expected search mode exit on Enter")
	}
}

// TestDynamicE2EWorkflow tests the entire interactive command suite over a real store
// and proves live state changes are reflected without restarting MARSHAL.
func TestDynamicE2EWorkflow(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "e2e_workspace.db")
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	projectID := "e2e-project"
	sessionID := "e2e-session-01"

	if err := st.InitProject(ctx, model.Project{
		ID:            projectID,
		Repository:    "/workspace/test",
		DefaultBranch: "main",
		PackVersion:   "1.5.0",
	}); err != nil {
		t.Fatalf("init project: %v", err)
	}

	ws := NewWorkspace(st, projectID, sessionID)

	// 1. Initial status
	out, err := ws.ExecuteCommand(ctx, "/status")
	if err != nil {
		t.Fatalf("/status failed: %v", err)
	}
	if !strings.Contains(out, "CANONICAL STATUS DETAIL") {
		t.Fatalf("unexpected /status output: %s", out)
	}

	// 2. TUI goal mutation fails closed; seed canonical state as test setup.
	out, err = ws.ExecuteCommand(ctx, "/goal Complete authorization refactoring safely")
	if err != nil {
		t.Fatalf("/goal set failed: %v", err)
	}
	if !strings.Contains(out, "unavailable") {
		t.Fatalf("unexpected /goal response: %s", out)
	}
	if err := st.SaveGoalContract(ctx, model.GoalContract{ID: "goal-e2e", SessionID: sessionID, DesiredOutcome: "Complete authorization refactoring safely", Risk: model.R1, AuthoritySource: "test", UnderstandingState: model.GoalReady}, 0); err != nil {
		t.Fatalf("seed goal: %v", err)
	}
	if err := ws.RefreshState(ctx); err != nil {
		t.Fatalf("refresh seeded goal: %v", err)
	}

	// 3. Inspect Goal
	out, err = ws.ExecuteCommand(ctx, "/goal")
	if err != nil {
		t.Fatalf("/goal inspect failed: %v", err)
	}
	if !strings.Contains(out, "Complete authorization refactoring safely") {
		t.Fatalf("unexpected /goal view: %s", out)
	}

	// 4. Check Agents
	out, err = ws.ExecuteCommand(ctx, "/agents")
	if err != nil {
		t.Fatalf("/agents failed: %v", err)
	}
	if !strings.Contains(out, "claude") || !strings.Contains(out, "codex") {
		t.Fatalf("expected participants in /agents output: %s", out)
	}

	// 5. TUI task mutation fails closed; seed canonical task as test setup.
	out, err = ws.ExecuteCommand(ctx, "/task create Verify token expiration boundary")
	if err != nil {
		t.Fatalf("/task create failed: %v", err)
	}
	if !strings.Contains(out, "unavailable") {
		t.Fatalf("unexpected /task create response: %s", out)
	}
	if _, err := st.ImportTasks(ctx, []model.Task{{ID: "TASK-E2E", Title: "Verify token expiration boundary", Status: model.TaskReady, Risk: model.R1}}); err != nil {
		t.Fatalf("seed task: %v", err)
	}

	out, err = ws.ExecuteCommand(ctx, "/tasks")
	if err != nil {
		t.Fatalf("/tasks list failed: %v", err)
	}
	if !strings.Contains(out, "Verify token expiration boundary") {
		t.Fatalf("expected task in /tasks list: %s", out)
	}

	// 6. Security policy inspection
	out, err = ws.ExecuteCommand(ctx, "/policy network")
	if err != nil {
		t.Fatalf("/policy network failed: %v", err)
	}
	if !strings.Contains(out, "NOT VERIFIED") {
		t.Fatalf("unexpected /policy network response: %s", out)
	}

	out, err = ws.ExecuteCommand(ctx, "/sandbox")
	if err != nil {
		t.Fatalf("/sandbox failed: %v", err)
	}
	if !strings.Contains(out, "NOT VERIFIED") {
		t.Fatalf("unexpected /sandbox response: %s", out)
	}

	// 7. Harness probe
	out, err = ws.ExecuteCommand(ctx, "/harness probe")
	if err != nil {
		t.Fatalf("/harness probe failed: %v", err)
	}
	if !strings.Contains(out, "HARNESS CAPABILITY PROBE") {
		t.Fatalf("unexpected /harness probe: %s", out)
	}

	// 8. Create Checkpoint
	out, err = ws.ExecuteCommand(ctx, "/checkpoint")
	if err != nil {
		t.Fatalf("/checkpoint failed: %v", err)
	}
	if !strings.Contains(out, "unavailable") {
		t.Fatalf("unexpected /checkpoint response: %s", out)
	}

	// 9. Live dynamic update without restart: create a claim directly in store
	goal := ws.GetUIState().Goal
	claim := model.Claim{
		ID:             "C-99",
		GoalID:         goal.ID,
		GoalRevision:   goal.Revision,
		Subject:        "auth_mutex",
		NormalizedText: "Token refresh mutex prevents race condition",
		Scope:          "auth",
		State:          model.ClaimStateVerified,
		Criticality:    model.CriticalityBlocker,
		Author: model.AuthorProvenance{
			AgentID: "codex",
			Harness: "codex",
		},
		CreatedAt: time.Now().UTC(),
	}
	if err := st.SaveClaim(ctx, claim); err != nil {
		t.Fatalf("save claim directly to store: %v", err)
	}

	// Workspace RefreshState
	if err := ws.RefreshState(ctx); err != nil {
		t.Fatalf("refresh state failed: %v", err)
	}
	uiState := ws.GetUIState()
	if len(uiState.Claims) != 1 || uiState.Claims[0].ID != "C-99" {
		t.Fatalf("expected claim C-99 in live workspace state, got %v", uiState.Claims)
	}

	// 10. Live approval workflow
	approval := model.Approval{
		ID:          "appr-01",
		ProjectID:   projectID,
		Operation:   model.Operation("out_of_scope_write"),
		Scope:       "auth",
		Target:      "internal/auth/token.go",
		RequestedBy: "codex",
		Status:      model.ApprovalRequested,
		CreatedAt:   time.Now().UTC(),
	}
	if err := st.CreateApproval(ctx, approval); err != nil {
		t.Fatalf("create approval in store: %v", err)
	}

	// TUI must not resolve approval without authenticated runtime identity.
	out, err = ws.ExecuteCommand(ctx, "/approve appr-01")
	if err != nil {
		t.Fatalf("/approve failed: %v", err)
	}
	if !strings.Contains(out, "unavailable") {
		t.Fatalf("unexpected /approve output: %s", out)
	}

	// Verify approval state in store
	resolvedAppr, err := st.GetApproval(ctx, "appr-01")
	if err != nil {
		t.Fatalf("get approval: %v", err)
	}
	if resolvedAppr.Status != model.ApprovalRequested {
		t.Fatalf("TUI mutated approval without authorization: %s", resolvedAppr.Status)
	}

	// 11. Run doctor from workspace
	out, err = ws.ExecuteCommand(ctx, "/doctor")
	if err != nil {
		t.Fatalf("/doctor failed: %v", err)
	}
	if !strings.Contains(out, "SYSTEM DIAGNOSTICS") {
		t.Fatalf("unexpected /doctor output: %s", out)
	}
}
