package tui

import "strings"

// Validate the work commands before dispatch so malformed input cannot act on a
// prefix while silently discarding the rest. Free-text arguments remain free text.
func workCommandUsage(parts []string) string {
	cmd, args := strings.ToLower(parts[0]), parts[1:]
	usage := ""
	valid := true
	switch cmd {
	case "/status", "/claims", "/agents", "/roster", "/why", "/budget", "/pause", "/cancel":
		usage, valid = cmd, len(args) == 0
	case "/mode":
		usage, valid = "/mode [manual|auto|ultra]", len(args) <= 1
	case "/approve":
		usage, valid = cmd+" <typed-id|id>", len(args) <= 1
	case "/reject":
		usage, valid = cmd+" <typed-id|id> [reason]", true
	case "/evidence", "/rollback":
		usage, valid = cmd+" <id>", len(args) == 1
	case "/inspect":
		usage = "/inspect [claim|evidence|checkpoint|task|handoff|approval|agent] <id>"
		valid = (len(args) == 1 && !workInspectKind(args[0])) || len(args) == 2
		if len(args) == 2 {
			valid = workInspectKind(args[0])
		}
	case "/msg", "/say":
		usage, valid = cmd+" <agent|all> <text>", len(args) >= 2
	case "/handoff":
		usage = "/handoff <architect|developer|qa|appsec> <summary>"
		valid = len(args) >= 2
		if valid {
			switch strings.ToLower(args[0]) {
			case "architect", "developer", "qa", "appsec":
			default:
				valid = false
			}
		}
	case "/goal":
		if len(args) > 0 {
			sub := strings.ToLower(args[0])
			switch sub {
			case "constraints":
				usage, valid = "/goal "+sub, len(args) == 1
			case "add-constraint", "rm-constraint":
				usage, valid = "/goal "+sub+" <text>", len(args) >= 2
			}
		}
	case "/checkpoint":
		usage = "/checkpoint list | inspect <id> | diff <from> <to> | create"
		if len(args) > 0 {
			switch strings.ToLower(args[0]) {
			case "list":
				valid = len(args) == 1
			case "inspect":
				valid = len(args) == 2
			case "diff":
				valid = len(args) == 3
			case "create":
				// Capture is not implemented; keep its explicit refusal.
			default:
				valid = false
			}
		}

	case "/tasks", "/task":
		usage = cmd + " [list|ownership] [--scope project|active] | create <title> | inspect <id> | assign <id> <agent> | pause|resume|cancel|retry <id>"
		if len(args) > 0 {
			switch strings.ToLower(args[0]) {
			case "--scope":
				valid = len(args) == 2
			case "list", "ownership":
				valid = len(args) == 1 || (len(args) == 3 && strings.EqualFold(args[1], "--scope"))
			case "create":
				valid = len(args) >= 2
			case "inspect", "pause", "resume", "cancel", "retry":
				valid = len(args) == 2
			case "assign":
				valid = len(args) == 3
			default:
				valid = false
			}
		}
	}
	if !valid {
		return "Usage: " + usage
	}
	return ""
}

func workInspectKind(kind string) bool {
	switch strings.ToLower(kind) {
	case "claim", "evidence", "checkpoint", "task", "handoff", "approval", "agent":
		return true
	}
	return false
}
