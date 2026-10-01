package tui

import "strings"

// Validate advertised configuration syntax before looking up a dependency or
// performing a side effect. Malformed input must never become a default action.
func configCommandUsage(parts []string) string {
	cmd, args := strings.ToLower(parts[0]), parts[1:]
	sub := ""
	if len(args) > 0 {
		sub = strings.ToLower(args[0])
	}
	valid, usage := true, ""
	switch cmd {
	case "/policy":
		usage = "/policy [network|sandbox|capability|scope|write|audit]"
		valid = len(args) == 0 || len(args) == 1 && oneOf(sub, "network", "sandbox", "capability", "scope", "write", "audit")
	case "/sandbox":
		usage = "/sandbox [read-only|workspace-write]"
		valid = len(args) == 0 || len(args) == 1 && oneOf(sub, "read-only", "workspace-write")
	case "/doctor":
		usage = "/doctor [codex|provider]"
		valid = len(args) == 0 || len(args) == 1 && oneOf(sub, "codex", "provider")
	case "/provider", "/providers":
		usage = "/provider [status|config <name>] (credentials must stay in the provider's own login flow)"
		valid = len(args) == 0 || sub == "status" && len(args) == 1 || sub == "config" && len(args) >= 2 // The handler explicitly refuses inline credentials.
	case "/harness":
		usage = "/harness [probe|status|select <role> <harness>]"
		valid = len(args) == 0 || oneOf(sub, "probe", "status") && len(args) == 1 || sub == "select" && len(args) == 3
	case "/model":
		usage = "/model [show|select <harness> <model_name>|<codex_slug>]"
		valid = len(args) == 0 || sub == "show" && len(args) == 1 || sub == "select" && len(args) == 3 || !oneOf(sub, "show", "select") && len(args) == 1
	case "/effort":
		usage = "/effort [minimal|low|medium|high|xhigh|default]"
		valid = len(args) == 0 || len(args) == 1 && oneOf(sub, "minimal", "low", "medium", "high", "xhigh", "default")
	case "/backup":
		usage = "/backup [create|restore <backup_path>]"
		valid = len(args) == 0 || sub == "create" && len(args) == 1 || sub == "restore" && len(args) == 2 && args[1] != ""
	case "/blind":
		usage = "/blind [resolve [reason ...]]"
		valid = len(args) == 0 || sub == "resolve"
	case "/alignment":
		usage = "/alignment [scope|violations|blast|deletions|status] | /alignment resolve run:<run>/<task>#<n> <acknowledged|goal-amendment-needed> <reason>"
		valid = len(args) == 0 || sub == "resolve" && len(args) >= 4 || len(args) == 1 && oneOf(sub, "scope", "violations", "blast", "deletions", "status")
	case "/features":
		usage = "/features [list|enable <feature>|disable <feature>]"
		valid = len(args) == 0 || sub == "list" && len(args) == 1 || oneOf(sub, "enable", "disable") && len(args) == 2
	case "/search":
		usage = "/search [on|off]"
		valid = len(args) == 0 || len(args) == 1 && oneOf(sub, "on", "off", "enable", "disable", "true", "false")
	case "/models", "/fingerprint", "/runtime", "/store", "/export", "/reinjection", "/login", "/logout":
		usage, valid = cmd, len(args) == 0
	}
	if !valid {
		return "Usage: " + usage
	}
	return ""
}

func oneOf(value string, options ...string) bool {
	for _, option := range options {
		if value == option {
			return true
		}
	}
	return false
}

const configStoreUnavailable = "Store unavailable. Open the TUI in an initialized MARSHAL project (marshal init)."
