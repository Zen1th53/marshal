package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/app"
)

// hasHelpFlag reports whether any argument is a standard help flag.
func hasHelpFlag(args []string) bool {
	for _, a := range args {
		if a == "-h" || a == "--help" {
			return true
		}
	}
	return false
}

// providerSubcommandSupportsHelp checks if the requested subcommand accepts -h/--help.
// For Antigravity, subcommands under plugin do not support --help flags; passing --help
// to them causes agy to treat "--help" as an argument (e.g. target name to uninstall).
func providerSubcommandSupportsHelp(provider string, args []string) bool {
	if provider == "agy" || provider == "antigravity" {
		if len(args) >= 2 && (args[0] == "plugin" || args[0] == "plugins") && !strings.EqualFold(args[1], "help") {
			return false
		}
	}
	return true
}

// isDestructiveNativeSubcommand returns true and the operation name if the subcommand
// is destructive (uninstall, remove, rm, delete, disable, logout across providers).
func isDestructiveNativeSubcommand(provider string, args []string) (bool, string) {
	norm := app.NormalizeProviderArgs(provider, args)
	if len(norm) == 0 {
		return false, ""
	}
	if len(norm) >= 2 {
		group := strings.ToLower(norm[0])
		verb := strings.ToLower(norm[1])
		switch group {
		case "mcp":
			switch provider {
			case "agy", "antigravity":
				if oneOf(verb, "remove", "rm", "delete", "disable") {
					return true, group + " " + verb
				}
			case "opencode":
				if verb == "logout" {
					return true, group + " " + verb
				}
			default:
				if oneOf(verb, "remove", "rm", "delete", "disable", "logout") {
					return true, group + " " + verb
				}
			}
		case "plugin", "plugins":
			if oneOf(verb, "uninstall", "remove", "rm", "delete", "disable") {
				return true, group + " " + verb
			}
		case "auth", "providers":
			if verb == "logout" {
				return true, group + " " + verb
			}
		}
	}
	return false, ""
}

// extractConfirmation strips --confirm or confirm from arguments if present,
// returning the cleaned arguments and whether confirmation was provided.
func extractConfirmation(args []string) ([]string, bool) {
	confirmed := false
	clean := make([]string, 0, len(args))
	for _, a := range args {
		if a == "--confirm" || a == "confirm" {
			confirmed = true
		} else {
			clean = append(clean, a)
		}
	}
	return clean, confirmed
}

// checkNativePassthroughSafety validates an operator native command before execution.
// A request containing -h/--help must only ever ask the provider for help.
// A destructive subcommand requires explicit operator confirmation (--confirm).
func checkNativePassthroughSafety(provider string, args []string) (cleanArgs []string, refusal string, isHelp bool) {
	d := app.ObserveProviderDialect(context.Background(), provider)
	op := app.ProviderArgOperation(provider, args)
	if d.Operation(op).Status == app.ProviderUnsupported {
		return args, fmt.Sprintf("Nothing was run. %s %s is UNSUPPORTED for %s", d.Provider, op, d.Version), false
	}

	if hasHelpFlag(args) {
		if !providerSubcommandSupportsHelp(provider, args) {
			sub := ""
			if len(args) > 1 {
				sub = " " + args[1]
			}
			return args, fmt.Sprintf("Antigravity plugin%s does not support --help. Use /agy plugin help for usage.", sub), true
		}
		return args, "", true
	}

	if destructive, opName := isDestructiveNativeSubcommand(provider, args); destructive {
		clean, confirmed := extractConfirmation(args)
		if !confirmed {
			return args, fmt.Sprintf("Destructive native subcommand %q requires explicit operator confirmation. Add --confirm to execute.", opName), false
		}
		return clean, "", false
	}

	return args, "", false
}
