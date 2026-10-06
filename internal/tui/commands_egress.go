package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/netpolicy"
	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/tmux"
)

const egressUsage = "Usage: /egress [status | allow <run-id> <host[:port]> | revoke <run-id> <host[:port]>] (omitted port means 443)"

func (h *CommandHandler) handleEgress(ctx context.Context, args []string) (string, error) {
	if len(args) != 0 && !(len(args) == 1 && args[0] == "status") && !(len(args) == 3 && (args[0] == "allow" || args[0] == "revoke")) {
		return egressUsage, nil
	}
	a, _ := h.ws.controlSource().Authority.(*runtimeControlAuthority)
	if a == nil || a.runtime == nil {
		return "Egress unavailable: no runtime is attached.", nil
	}
	if len(args) == 3 {
		if a.localControl == nil {
			return "Egress decision refused: authenticated local operator required.", nil
		}
		if err := a.runtime.CommandEgress(a.localControl.Context(ctx), args[1], args[0], args[2]); err != nil {
			return "Egress decision refused: " + err.Error(), nil
		}
		return fmt.Sprintf("Egress %s recorded for %s: %s. The worker may retry a refused request.", args[0], args[1], args[2]), nil
	}
	var b strings.Builder
	b.WriteString("Governed workers: isolated network namespace; per-run Unix proxy; exact endpoints; operator grants only.\nNative sessions opened directly by the operator: out of scope.\n")
	if !app.EgressEnforcementAvailable() {
		b.WriteString("Network work is refused here: bubblewrap network isolation or the trusted socat bridge is unavailable.\n")
	}
	rows := a.runtime.EgressStatus()
	if len(rows) == 0 {
		b.WriteString("No active governed egress runs.\n")
	}
	for _, row := range rows {
		fmt.Fprintf(&b, "%s: %s (%s), task %s, parent %s\n  Allowed: %s\n", row.RunID, row.Worker, row.Provider, row.TaskID, row.ParentRunID, strings.Join(row.Allowed, ", "))
		for _, endpoint := range row.Pending {
			if _, err := netpolicy.Endpoint(endpoint); err == nil {
				fmt.Fprintf(&b, "  %s wants to reach %s. Allow?\n  /egress allow %s %s\n", row.Worker, endpoint, row.RunID, endpoint)
			} else {
				fmt.Fprintf(&b, "  Direct socket operation refused: %s. Use the run proxy.\n", endpoint)
			}
		}
	}
	alerts, err := a.runtime.EgressNotifications(ctx)
	if err != nil {
		return "Egress notification inbox unavailable: " + err.Error(), nil
	}
	if len(alerts) > 0 {
		b.WriteString("Durable refusal inbox (including completed runs):\n")
	}
	for _, alert := range alerts {
		fmt.Fprintf(&b, "  %s: %s\n", alert.RunID, alert.Message)
	}
	b.WriteString(egressUsage)
	return b.String(), nil
}

// deliverEgressAlert renders a refused request in the TUI and in the Marshal's
// chat inbox. Native chat delivery is a pull, as with the shared channel.
func (w *Workspace) deliverEgressAlert(alert app.EgressAlert) error {
	w.egressMu.Lock()
	defer w.egressMu.Unlock()
	if alert.Kind == "" {
		alert.Kind = "egress refused"
	}
	if alert.State == "" {
		alert.State = "waiting"
	}
	key := alert.RunID + ":" + alert.TaskID + ":" + alert.Kind + ":" + alert.Message
	w.tmuxMu.Lock()
	if w.tmuxDelivered == nil {
		w.tmuxDelivered = make(map[string]bool)
	}
	if w.tmuxDelivered[key] {
		w.tmuxMu.Unlock()
		return nil
	}
	w.tmuxMu.Unlock()
	view, err := openInboxView(w.workDir, "marshal", false)
	if err != nil {
		return err
	}
	text := fmt.Sprintf("%s\nRun: %s\n", alert.Message, alert.RunID)
	if _, err := netpolicy.Endpoint(alert.Endpoint); err == nil && alert.State == "waiting" && (w.runtime == nil || w.runtime.EgressRequestPending(alert.RunID, alert.Endpoint)) {
		w.queuePermission(permission.Request{Kind: "network", Object: alert.Endpoint, Scope: "this worker run only, network access", Who: alert.Worker, Reason: alert.Message, RunID: alert.RunID, TaskID: alert.TaskID})
		text += fmt.Sprintf("Operator action: /egress allow %s %s\nModels may relay this request; only the operator may grant.\n", alert.RunID, alert.Endpoint)
	}
	if alert.State == "expired" {
		text += "Permission request expired; access remains denied.\n"
	}
	if err := view.write(fmt.Sprintf("## runtime · %s · %s\n\n%s\n", time.Now().UTC().Format(time.RFC3339), alert.Kind, text)); err != nil {
		return err
	}
	if err := view.trim(); err != nil {
		return err
	}
	w.tmuxMu.Lock()
	w.tmuxDelivered[key] = true
	if w.tmuxAlerts == nil {
		w.tmuxAlerts = make(map[string]string)
	}
	w.tmuxAlerts[alertScope(alert)+":"+alert.TaskID] = alert.Kind + ": " + alert.State
	var focus string
	for _, a := range w.tmuxActiveWins {
		if a.role == "marshal-chat" {
			continue
		}
		if alertMatchesAgent(alert, a) {
			a.state = alert.State
			if w.tmuxFollowActive && alert.State == "waiting" {
				focus = a.paneID
			}
		}
	}
	session := w.tmuxSession
	path := w.tmuxPath
	w.tmuxMu.Unlock()
	if path != "" && session != "" {
		w.updateTmuxStatusLine(context.Background())
		if focus != "" {
			_ = tmux.SelectWindow(context.Background(), focus)
			_ = tmux.SelectPane(context.Background(), focus)
		}
	}
	w.mu.Lock()
	w.state.LastOutput = text
	w.mu.Unlock()
	w.renderFullView()
	return nil
}

func alertScope(alert app.EgressAlert) string {
	if alert.ParentRunID != "" {
		return alert.ParentRunID
	}
	return alert.RunID
}
func alertMatchesAgent(alert app.EgressAlert, a *activeTmuxAgent) bool {
	scope := alertScope(alert)
	if alert.TaskID != "" {
		return (a.taskID == alert.TaskID && a.runID == scope) || (a.canonicalTaskID == alert.TaskID && a.executionRunID == scope)
	}
	return a.role == "worker" && a.provider == alert.Worker && a.runID == scope
}
