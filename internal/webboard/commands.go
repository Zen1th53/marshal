package webboard

import "strings"

func validCommandTarget(c Command) bool {
	switch c.Target.Kind {
	case "setting", "entitlement-request":
		return c.RunID == "" && c.AttemptID == "" && strings.HasPrefix(c.Op, "settings/")
	case "run":
		return c.Target.ID != "" && c.Target.ID == c.RunID && c.AttemptID != "" && strings.HasPrefix(c.Op, "run/")
	case "team":
		return c.Target.ID != "" && c.RunID == "" && c.AttemptID != "" && strings.HasPrefix(c.Op, "team/")
	case "task", "lane":
		return c.Target.ID != "" && c.RunID != "" && c.AttemptID != "" && strings.HasPrefix(c.Op, c.Target.Kind+"/")
	}
	return false
}

func validWorkPayload(c Command) bool {
	p := c.Payload
	if p.Key != "" || p.Value != "" || p.Enabled != nil || len(p.Reason) > 2000 || len(p.Provider) > 128 || len(p.Model) > 128 {
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

// TeamOps are the provider, session and architecture actions of the Team and
// Architecture pages. They are bound to the state they were offered for (the
// attempt id) rather than to a run, so they work before any run exists.
var TeamOps = []string{"team/provider.verify", "team/provider.route-around", "team/provider.signin.start", "team/session.import.request", "team/architecture.open", "team/telemetry.choose"}

// IsTeamOp reports whether op is one of TeamOps.
func IsTeamOp(op string) bool {
	for _, o := range TeamOps {
		if o == op {
			return true
		}
	}
	return false
}

// validTeamPayload admits only the identity the capability named: a provider
// name, an opaque candidate id or nothing. A path, a credential or a free
// value is never part of a team action.
func validTeamPayload(c Command) bool {
	p := c.Payload
	if p.Key != "" || p.Model != "" || p.ApprovalID != "" || len(p.Reason) > 2000 || len(p.Provider) > 128 {
		return false
	}
	switch c.Op {
	case "team/provider.verify", "team/provider.route-around", "team/provider.signin.start":
		return p.Provider != "" && p.Provider == c.Target.ID && p.Value == "" && p.Enabled == nil
	case "team/session.import.request", "team/architecture.open":
		return p.Provider == "" && p.Value == "" && p.Enabled == nil
	case "team/telemetry.choose":
		return p.Provider == "" && p.Enabled == nil && (p.Value == "keep" || p.Value == "off")
	}
	return false
}
