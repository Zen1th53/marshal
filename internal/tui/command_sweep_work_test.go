//go:build linux

package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/protocol"
	"github.com/Zen1th53/marshal/internal/testutil/testcloud"
)

func sweepWorkRun(t *testing.T, ws *Workspace, line, want string) string {
	t.Helper()
	out, err := ws.ExecuteCommand(context.Background(), line)
	if err != nil {
		t.Fatalf("%s: %v", line, err)
	}
	if out == "" || !strings.Contains(out, want) {
		t.Fatalf("%s: want %q, got %q", line, want, out)
	}
	return out
}

// Every root command is exercised with no args, a valid path, and malformed
// input against both a canonical store and NewWorkspace(nil, ...). Neither
// workspace has entitlement, a run, or installed providers.
func TestCommandSweepWorkRoots(t *testing.T) {
	sweepWorkEnvironment(t)
	_, stored, _ := newControlWorkspace(t)
	stored.workDir = t.TempDir()
	useCodexByDefault(t, stored.workDir)
	empty := NewWorkspace(nil, "sweep", "sweep")
	empty.workDir = t.TempDir()
	useCodexByDefault(t, empty.workDir)
	cases := []struct{ command, bare, valid, want, bad string }{
		{"/status", "CANONICAL STATUS DETAIL", "/status", "NO ACTIVE GOAL", "/status extra"},
		{"/goal", "No active goal", "/goal desired outcome", "unavailable", "/goal constraints extra"},
		{"/mode", "Current mode", "/mode auto", "AUTO", "/mode manual extra"},
		{"/claims", "No claims", "/claims", "No claims", "/claims extra"},
		{"/inspect", "Usage:", "/inspect task missing", "", "/inspect typo id"},
		{"/approve", "unavailable", "/approve approval", "unavailable", "/approve id extra"},
		{"/reject", "unavailable", "/reject approval reason", "unavailable", ""},
		{"/route", "ADVISORY", "/route role=qa risk=R2 harness=codex", "ADVISORY", "/route typo"},
		{"/agents", "TEAM ROSTER", "/agents", "unavailable", "/agents extra"},
		{"/evidence", "Usage:", "/evidence missing", "NOT FOUND", "/evidence id extra"},
		{"/why", "entitlement", "/why", "entitlement", "/why extra"},
		{"/msg", "Usage:", "/msg all guidance", "unavailable", "/msg all"},
		{"/handoff", "Usage:", "/handoff qa review it", "unavailable", "/handoff typo summary"},
		{"/checkpoint", "unavailable", "/checkpoint create name", "unavailable", "/checkpoint typo"},
		{"/rollback", "Usage:", "/rollback cp", "NOT performed", "/rollback cp extra"},
		{"/budget", "BUDGET CONSUMED", "/budget", "UNKNOWN", "/budget 123"},
		{"/pause", "NOT performed", "/pause", "NOT performed", "/pause extra"},
		{"/resume", "NOT performed", "/resume --last", "authority unavailable", ""},
		{"/cancel", "NOT performed", "/cancel", "NOT performed", "/cancel extra"},
		{"/tasks", "", "/tasks list", "", "/tasks typo"},
		{"/task", "", "/task ownership", "", "/task typo"},
	}
	for _, variant := range []struct {
		name string
		ws   *Workspace
	}{{"store", stored}, {"nil-store", empty}} {
		t.Run(variant.name, func(t *testing.T) {
			for _, c := range cases {
				t.Run(c.command, func(t *testing.T) {
					sweepWorkRun(t, variant.ws, c.command, c.bare)
					sweepWorkRun(t, variant.ws, c.valid, c.want)
					if c.bad != "" {
						sweepWorkRun(t, variant.ws, c.bad, "Usage:")
					}
					sweepWorkRun(t, variant.ws, c.command+"typo", "Unknown command")
				})
			}
		})
	}
	sweepWorkRun(t, stored, "/resume typo extra --unknown", "authority unavailable")
	sweepWorkRun(t, stored, "/mode manual", "MANUAL")
	sweepWorkRun(t, stored, "/mode auto", "AUTO")
	sweepWorkRun(t, stored, "/mode typo", "Invalid mode")
	sweepWorkRun(t, stored, "/mode ultra", "unavailable")
	if stored.mode != "auto" {
		t.Fatalf("rejected mode changed state: %s", stored.mode)
	}
	for _, line := range []string{"/route role=typo", "/route risk=R9", "/route unknown=value", "/route role=qa extra"} {
		out := sweepWorkRun(t, stored, line, "")
		if strings.Contains(out, "ADVISORY ONLY") {
			t.Fatalf("malformed route was computed: %s", out)
		}
	}
}

func TestCommandSweepWorkSubcommands(t *testing.T) {
	sweepWorkEnvironment(t)
	st, ws, ctx := newControlWorkspace(t)
	ws.workDir = t.TempDir()
	if _, err := st.ImportTasks(ctx, []model.Task{{ID: "TASK-sweep", Title: "Review sweep", Status: model.TaskReady, Risk: model.R1, Revision: 1}}); err != nil {
		t.Fatal(err)
	}
	goal := model.GoalContract{ID: "goal-sweep", SessionID: ws.sessionID, DesiredOutcome: "Keep outcome", Risk: model.R1, AuthoritySource: "test", UnderstandingState: model.GoalReady, Constraints: []model.Constraint{{ID: "c-sweep", Text: "Stay offline", IsHard: true}}}
	if err := st.SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatal(err)
	}
	if err := ws.RefreshState(ctx); err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{"create", "edit", "diff", "version", "criteria", "constraints", "add-constraint", "rm-constraint", "donotdo", "progress"} {
		t.Run("goal/"+sub, func(t *testing.T) {
			line := "/goal " + sub
			arg := ""
			if sub == "create" || sub == "edit" || sub == "add-constraint" || sub == "rm-constraint" {
				arg = " text"
			}
			sweepWorkRun(t, ws, line, "")
			sweepWorkRun(t, ws, line+arg, "")
			sweepWorkRun(t, ws, line+" extra unknown", "")
			sweepWorkRun(t, ws, line+"typo", "unavailable") // free-text fallback, tracked as a decision
		})
	}
	sweepWorkRun(t, ws, "/goal", "Keep outcome")
	sweepWorkRun(t, ws, "/goal constraints", "Stay offline")
	for _, sub := range []string{"list", "create", "inspect", "diff"} {
		t.Run("checkpoint/"+sub, func(t *testing.T) {
			sweepWorkRun(t, ws, "/checkpoint "+sub, "")
			arg := ""
			switch sub {
			case "inspect", "create":
				arg = " cp-sweep"
			case "diff":
				arg = " cp-sweep cp-other"
			}
			sweepWorkRun(t, ws, "/checkpoint "+sub+arg, "unavailable")
			sweepWorkRun(t, ws, "/checkpoint "+sub+" id extra", "")
			sweepWorkRun(t, ws, "/checkpoint "+sub+"typo", "Usage:")
		})
	}
	for _, cmd := range []string{"/task", "/tasks"} {
		for _, sub := range []string{"list", "create", "inspect", "assign", "pause", "resume", "cancel", "retry", "ownership"} {
			t.Run(cmd+"/"+sub, func(t *testing.T) {
				arg := " TASK-sweep"
				switch sub {
				case "list", "ownership":
					arg = ""
				case "assign":
					arg += " codex"
				case "create":
					arg = " title with spaces"
				}
				sweepWorkRun(t, ws, cmd+" "+sub, "")
				sweepWorkRun(t, ws, cmd+" "+sub+arg, "")
				sweepWorkRun(t, ws, cmd+" "+sub+arg+" extra", "")
				sweepWorkRun(t, ws, cmd+" "+sub+"typo", "Usage:")
			})
		}
	}
	sweepWorkRun(t, ws, "/task inspect TASK-sweep", "Review sweep")
	sweepWorkRun(t, ws, "/tasks LIST", "Review sweep")
	sweepWorkRun(t, ws, "/task ownership", "WORK OWNERSHIP TABLE")
	task, err := st.GetTask(ctx, "TASK-sweep")
	if err != nil || task.Status != model.TaskReady || task.OwnerAgentID != nil {
		t.Fatalf("refused mutation changed task: %#v %v", task, err)
	}
	current, err := st.GetActiveGoalContract(ctx, ws.sessionID)
	if err != nil || current.DesiredOutcome != "Keep outcome" || len(current.Constraints) != 1 {
		t.Fatalf("refused mutation changed goal: %#v %v", current, err)
	}
}

func TestCommandSweepWorkInspectAndDiscovery(t *testing.T) {
	sweepWorkEnvironment(t)
	st, ws, ctx := newControlWorkspace(t)
	ws.workDir = t.TempDir()
	claim := model.Claim{ID: "claim-sweep", GoalID: "goal-sweep", GoalRevision: 1, NormalizedText: "Evidence supports this", State: model.ClaimStateSupported, Subject: "sweep", Scope: "sweep", Criticality: model.CriticalityStandard, Author: model.AuthorProvenance{AgentID: "test"}, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), SupportingEvidence: []model.EvidenceRef{{EvidenceID: "ev-sweep", Tool: "test"}}}
	if err := st.SaveClaim(ctx, claim); err != nil {
		t.Fatal(err)
	}
	ws.state.Claims = []model.Claim{claim}
	sweepWorkRun(t, ws, "/claims", "claim-sweep")
	sweepWorkRun(t, ws, "/evidence ev-sweep", "supports Claim claim-sweep")
	sweepWorkRun(t, ws, "/inspect ev-sweep", "supports Claim claim-sweep")
	sweepWorkRun(t, ws, "/inspect #claim-sweep", "CLAIM claim-sweep")
	sweepWorkRun(t, ws, "/evidence #ev-sweep", "supports Claim claim-sweep")
	sweepWorkRun(t, ws, "/inspect evidence #ev-sweep", "supports Claim claim-sweep")
	sweepWorkRun(t, ws, "/rollback #cp-sweep", "Rollback to cp-sweep")
	sweepWorkRun(t, ws, "/inspect missing", "No canonical record")
	for _, kind := range []string{"claim", "evidence", "checkpoint", "task", "handoff", "approval", "agent"} {
		sweepWorkRun(t, ws, "/inspect "+kind, "Usage:")
		sweepWorkRun(t, ws, "/inspect "+kind+" missing", "No "+kind+" found")
		sweepWorkRun(t, ws, "/inspect "+kind+" missing extra", "Usage:")
	}
	if _, err := st.ImportTasks(ctx, []model.Task{{ID: "TASK-inspect", Title: "Inspection target", Status: model.TaskReady, Risk: model.R1}}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveHandoffCheckpoint(ctx, model.HandoffCheckpoint{ID: "CP-sweep", Version: 1, SessionID: ws.sessionID, TaskID: "TASK-inspect", Role: "operator", Author: model.AuthorProvenance{AgentID: "test"}, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	seedPendingApproval(t, st, ctx, "approval-sweep")
	if _, err := st.Create(ctx, protocol.Handoff{ID: "HANDOFF-sweep", Version: protocol.Version1, TaskID: "TASK-inspect", FromAgent: "developer", ToRole: protocol.RoleQA, Status: protocol.StatusCreated, ContextDigest: "sha256:" + strings.Repeat("a", 64), IdempotencyKey: "sweep-handoff", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ line, want string }{
		{"/inspect claim claim-sweep", "CLAIM claim-sweep"}, {"/inspect task #TASK-inspect", "Inspection target"},
		{"/inspect checkpoint CP-sweep", "CHECKPOINT CP-sweep"}, {"/inspect handoff HANDOFF-sweep", "HANDOFF HANDOFF-sweep"},
		{"/inspect approval approval-sweep", "APPROVAL approval-sweep"}, {"/inspect agent codex", "AGENT codex"},
	} {
		sweepWorkRun(t, ws, c.line, c.want)
	}
	for _, cmd := range []string{"/approve", "/reject"} {
		sweepWorkRun(t, ws, cmd+" approval-sweep", "unavailable")
		record, err := st.GetApproval(ctx, "approval-sweep")
		if err != nil || record.Status != model.ApprovalRequested {
			t.Fatalf("refused approval changed: %#v %v", record, err)
		}
	}
	// Completion reads exactly the references displayed by the claim commands.
	for _, line := range []string{"/inspect #claim-", "/evidence #ev-", "/task inspect #TASK-"} {
		_, matches := ws.completer.Suggest(line, len([]rune(line)))
		if len(matches) != 1 {
			t.Fatalf("%s completion: %v", line, matches)
		}
	}
	for _, cmd := range []string{"/mode", "/inspect", "/task", "/tasks"} {
		_, matches := ws.completer.Suggest(cmd+" ", len([]rune(cmd+" ")))
		if len(matches) == 0 {
			t.Fatalf("missing argument completions for %s", cmd)
		}
	}

	help := sweepWorkRun(t, ws, "/help", "")
	for _, cmd := range []string{"/status", "/goal", "/mode", "/claims", "/inspect", "/approve", "/reject", "/route", "/agents", "/evidence", "/why", "/msg", "/handoff", "/checkpoint", "/rollback", "/budget", "/pause", "/resume", "/cancel", "/tasks", "/task"} {
		if !strings.Contains(help, cmd) || !ws.knownCommand(cmd) {
			t.Errorf("missing help/completion: %s", cmd)
		}
	}
}

func TestCommandSweepWorkPTY(t *testing.T) {
	// Build before restricting PATH/HOME, preserving the offline module cache.
	bin := buildMarshalBinary(t)
	sweepWorkEnvironment(t)
	project := initProject(t, bin)
	useCodexByDefault(t, project)

	cases := []struct{ line, want string }{
		{"/status", "CANONICAL STATUS DETAIL"}, {"/goal", "No active goal"}, {"/mode auto", "Operating mode switched"},
		{"/claims", "No claims"}, {"/inspect task missing", "No task found"}, {"/approve apr", "Error: not found"},
		{"/reject apr", "Error: not found"}, {"/route role=qa", "ADVISORY ONLY"}, {"/agents", "TEAM ROSTER"},
		{"/evidence missing", "NOT FOUND"}, {"/why", "No ULTRA route explanation"}, {"/msg all guidance", "nothing was sent"},
		{"/handoff qa review", "nothing was sent"}, {"/checkpoint list", "No execution snapshots in this project"},
		{"/rollback cp", "NOT performed"}, {"/budget", "BUDGET CONSUMED"}, {"/pause", "Pause was NOT performed"},
		{"/resume --last", "Install Codex"}, {"/cancel", "Cancel was NOT performed"}, {"/tasks", "No tasks in store"},
		{"/task ownership", "WORK OWNERSHIP TABLE"},
	}
	for _, c := range cases {
		t.Run(c.line, func(t *testing.T) {
			s := startFrozenTUIInProject(t, 50, 180, bin, project, "tui")
			// Dismiss completion before Enter: the typed command must submit intact.
			before := len(s.output())
			s.send(c.line)
			s.send("\x1b")
			s.send("\r")
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				if strings.Contains(StripANSI(s.output()[before:]), c.want) {
					return
				}
				time.Sleep(40 * time.Millisecond)
			}
			t.Fatalf("%s: want %q, terminal tail:\n%s", c.line, c.want, tail(s.output()[before:], 4000))
		})
	}
}

// The complete advertised subcommand inventory is also run with no canonical
// store and with a store but no goal/session/run. These cases cannot use a
// provider or persist a successful mutation.
func TestCommandSweepWorkDegradedSubcommands(t *testing.T) {
	sweepWorkEnvironment(t)
	_, stored, _ := newControlWorkspace(t)
	stored.workDir = t.TempDir()
	absent := NewWorkspace(nil, "sweep", "sweep")
	absent.workDir = t.TempDir()
	for _, ws := range []*Workspace{stored, absent} {
		for _, cmd := range []string{"/goal", "/checkpoint", "/task", "/tasks"} {
			for _, sub := range ws.completer.ctx.Subcommands[cmd] {
				line := cmd + " " + sub
				for _, suffix := range []string{"", " id", " id agent", " id agent extra"} {
					out := sweepWorkRun(t, ws, line+suffix, "")
					if strings.Contains(out, "created:") || strings.Contains(out, "transitioned") || strings.Contains(out, "claimed by") {
						t.Fatalf("degraded command claimed success: %s", out)
					}
				}
				sweepWorkRun(t, ws, line+"typo", "")
			}
		}
	}
}

func TestCommandSweepWorkBudgetKnownZero(t *testing.T) {
	sweepWorkEnvironment(t)
	ws := NewWorkspace(nil, "sweep", "sweep")
	ws.workDir = t.TempDir()
	sweepWorkRun(t, ws, "/status", "tokens=UNKNOWN cost=UNKNOWN")
	zeroTokens := int64(0)
	zeroCost := float64(0)
	ws.state.BudgetConsumed.TotalTokens = &zeroTokens
	ws.state.BudgetConsumed.CostUSD = &zeroCost
	sweepWorkRun(t, ws, "/budget", "Tokens: 0")
	sweepWorkRun(t, ws, "/budget", "Cost: $0.0000")
}

func TestCommandSweepWorkRevisionRefresh(t *testing.T) {
	sweepWorkEnvironment(t)
	st, ws, ctx := newControlWorkspace(t)
	ws.workDir = t.TempDir()
	goal := model.GoalContract{ID: "GOAL-revision", SessionID: ws.sessionID, DesiredOutcome: "First outcome", Risk: model.R1, AuthoritySource: "test", UnderstandingState: model.GoalReady}
	if err := st.SaveGoalContract(ctx, goal, 0); err != nil {
		t.Fatal(err)
	}
	saved, err := st.GetActiveGoalContract(ctx, ws.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	tokens := int64(321)
	if err := st.SaveBudgetTracker(ctx, ws.sessionID, saved.ID, saved.Revision, model.ConsumedBudget{TotalTokens: &tokens}); err != nil {
		t.Fatal(err)
	}
	sweepWorkRun(t, ws, "/budget", "Tokens: 321")
	ws.state.TerminationState = model.TerminationState("SUCCESS")
	saved.DesiredOutcome = "Second outcome"
	saved.Revision++
	if err := st.SaveGoalContract(ctx, saved, saved.Revision-1); err != nil {
		t.Fatal(err)
	}
	sweepWorkRun(t, ws, "/goal", "Second outcome")
	sweepWorkRun(t, ws, "/budget", "Tokens: UNKNOWN")
	if ws.state.TerminationState != "" {
		t.Fatalf("old termination leaked into new revision: %s", ws.state.TerminationState)
	}
}

func TestCommandSweepWorkInspectStoreFailure(t *testing.T) {
	sweepWorkEnvironment(t)
	st, ws, ctx := newControlWorkspace(t)
	ws.workDir = t.TempDir()
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"/inspect claim missing", "/inspect missing", "/task inspect TASK-missing", "/status", "/claims", "/budget"} {
		out, err := ws.ExecuteCommand(ctx, line)
		if err == nil || !strings.Contains(err.Error(), "reopen the TUI") || strings.Contains(out, "found") {
			t.Fatalf("%s hid store failure: %q %v", line, out, err)
		}
	}
}

func TestCommandSweepWorkEntitlement(t *testing.T) {
	sweepWorkEnvironment(t)
	ws := NewWorkspace(nil, "sweep", "sweep")
	ws.workDir = t.TempDir()
	ws.AttachULTRA(testcloud.EntitledGate(t, testcloud.Options{}), false)
	sweepWorkRun(t, ws, "/mode", "ULTRA entitled: true")
	sweepWorkRun(t, ws, "/mode ultra", "switched to ULTRA")
	sweepWorkRun(t, ws, "/why", "ADVISORY ROUTING EXPLANATION (NOT APPLIED)")
	for _, line := range []string{"/goal create outcome", "/approve id", "/reject id", "/msg all guidance", "/handoff qa review", "/task create title", "/checkpoint create name"} {
		sweepWorkRun(t, ws, line, "unavailable")
	}
	ws.AttachULTRA(nil, false)
	sweepWorkRun(t, ws, "/mode ultra", "unavailable")
	sweepWorkRun(t, ws, "/why", "entitlement is not active")
	ws.router = nil
	sweepWorkRun(t, ws, "/route", "Advisory router unavailable")
}

func TestCommandSweepWorkAgentAvailability(t *testing.T) {
	sweepWorkEnvironment(t)
	_, ws, ctx := newControlWorkspace(t)
	ws.workDir = t.TempDir()
	ws.state.ActiveTurn = "codex"
	out := sweepWorkRun(t, ws, "/inspect agent codex", "UNAVAILABLE")
	if strings.Contains(out, "WORKING") || strings.Contains(out, "Recent Handoffs: NONE") {
		t.Fatalf("fabricated agent activity: %s", out)
	}
	// Test the installed-provider observation without running a provider CLI.
	ws.state.Participants = []model.Participant{{AgentID: "codex", IsActive: true}}
	out, err := ws.cmd.inspectOne(ctx, "agent", "codex")
	if err != nil || !strings.Contains(out, "ACTIVE TURN (execution not verified)") {
		t.Fatalf("availability claimed execution: %q %v", out, err)
	}
}
