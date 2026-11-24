package webboard

import (
	"strings"
	"unicode/utf8"
)

func validCommandTarget(c Command) bool {
	switch c.Target.Kind {
	case "setting", "entitlement-request":
		return c.RunID == "" && c.AttemptID == "" && strings.HasPrefix(c.Op, "settings/")
	case "run":
		return c.Target.ID != "" && c.Target.ID == c.RunID && c.AttemptID != "" && strings.HasPrefix(c.Op, "run/")
	case "task", "lane":
		return c.Target.ID != "" && c.RunID != "" && c.AttemptID != "" && strings.HasPrefix(c.Op, c.Target.Kind+"/")
	}
	return false
}

func validWorkPayload(c Command) bool {
	p := c.Payload
	switch c.Op {
	case "run/chat.send", "run/plan.comment", "run/architecture.comment", "run/goal.intake", "run/goal.amend.propose":
		// An owner message: its words only, bounded like the Board's own say.
		return c.Target.Kind == "run" && strings.TrimSpace(p.Text) != "" && utf8.RuneCountInString(p.Text) <= SayMax &&
			p.Key == "" && p.Value == "" && p.Enabled == nil && p.Reason == "" && p.Provider == "" && p.Model == "" && p.ApprovalID == ""
	}
	if p.Text != "" || p.Key != "" || p.Value != "" || p.Enabled != nil || len(p.Reason) > 2000 || len(p.Provider) > 128 || len(p.Model) > 128 {
		return false
	}
	switch c.Op {
	case "task/reassign", "task/model.set":
		return p.Provider != "" && p.Model == "" && p.ApprovalID == ""
	case "task/return":
		return strings.TrimSpace(p.Reason) != "" && p.Provider == "" && p.Model == "" && p.ApprovalID == ""
	case "task/approve-task":
		return p.ApprovalID != "" && p.Provider == "" && p.Model == ""
	case "task/retry", "task/retry.debug", "task/skip", "task/accept", "lane/nudge", "lane/restart", "run/pause", "run/stop", "run/resume", "run/stop-workers", "run/amend.deny":
		return p.Provider == "" && p.Model == "" && p.ApprovalID == ""
	}
	return false
}
