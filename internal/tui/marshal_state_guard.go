package tui

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/Zen1th53/marshal/internal/statepath"
)

// The Marshal chat reads MARSHAL's state through MARSHAL's tools only. Its
// state database, the database's journal files, the knowledge index and the
// database backups are MARSHAL's private state: a Marshal that opens them
// itself (agy once ran sqlite3 on .marshal/state.db to work out a decision)
// reads around the tools and can act on a half-written or stale state. Where
// the provider CLI offers a per-launch rule, the Marshal chat is started so
// that it cannot read or write them:
//
//   - Antigravity: deny rules in the session-only --settings
//     (antigravityMarshalStateDeny), for its file tools and, through its
//     terminal sandbox, for shell commands; sqlite3 is denied outright.
//   - Codex: "none" entries in MARSHAL's read-only permission profile
//     (codexMarshalConfineFlags), which its sandbox applies to every shell
//     command. Only while that profile is on: with execution rights
//     small-tasks the launch keeps the person's own sandbox and only the
//     protocol rule holds.
//   - Claude Code: Read and Edit deny rules and Bash(sqlite3:*) in the
//     launch's --settings (claudeMarshalStateSettings). Claude applies a Read
//     deny to its file tools and to shell reads of the file such as cat.
//   - OpenCode: nothing is added. Its permission schema could not be checked
//     against an installed CLI, and an unknown key can refuse the whole
//     configuration, so only the protocol rule holds there.
//
// MARSHAL's MCP server is started by the provider outside its sandbox, so
// the tools keep their access.
//
// The architecture review's directory (.marshal/architecture/) is private
// state as well: its review state is the owner's Accept record, and the
// Marshal reads the architecture through its tools only. tmux is denied as a
// command too: the Board runs in a pane of MARSHAL's tmux server, and a
// Marshal that reads or types into that pane acts as the owner. Codex's
// read-only profile already refuses the tmux socket (its sandbox has no
// network), so only Claude and agy get a command rule.

// marshalPrivateStatePaths are the paths under .marshal/ only MARSHAL's tools
// read or write. A path ending in "/" is a directory.
func marshalPrivateStatePaths() []string {
	return []string{
		"state.db",
		"state.db-wal",
		"state.db-shm",
		"state.db-journal",
		"knowledge/",
		"backups/",
		"architecture/",
	}
}

// codexMarshalStateFilesystem are the permission-profile entries that take
// every access to MARSHAL's private state from the Codex Marshal chat, in
// the profile's workspace-relative form.
func codexMarshalStateFilesystem() []string {
	var entries []string
	for _, rel := range marshalPrivateStatePaths() {
		entries = append(entries, fmt.Sprintf("%q=\"none\"", statepath.DirName+"/"+strings.TrimSuffix(rel, "/")))
	}
	return entries
}

// claudeMarshalStateDenyRules are the Claude Code deny rules for MARSHAL's
// private state, relative to the project (the launch's working directory).
// One "state.db*" pattern covers the database and its journal files.
func claudeMarshalStateDenyRules() []string {
	patterns := []string{"./" + statepath.DirName + "/state.db*"}
	for _, rel := range marshalPrivateStatePaths() {
		if strings.HasSuffix(rel, "/") {
			patterns = append(patterns, "./"+statepath.DirName+"/"+rel+"**")
		}
	}
	var rules []string
	for _, p := range patterns {
		rules = append(rules, "Read("+p+")", "Edit("+p+")")
	}
	return append(rules, claudeMarshalDenyCommands...)
}

// claudeMarshalDenyCommands are the shell commands the Claude Marshal chat may
// never run: sqlite3 reads MARSHAL's state database behind its tools, and
// tmux reads or types into the owner's Board pane. WebFetch of this
// computer's loopback is denied too: the owner's review page and Web Board
// listen there, and the Marshal has no business with either.
var claudeMarshalDenyCommands = []string{"Bash(sqlite3:*)", "Bash(tmux:*)", "WebFetch(domain:127.0.0.1)", "WebFetch(domain:localhost)"}

// claudeMarshalStateSettings adds claudeMarshalStateDenyRules to a Claude
// Code --settings JSON object (empty for none) and returns it, or false when
// the value is not a JSON object (a settings file the person named).
func claudeMarshalStateSettings(value string) (string, bool) {
	settings := map[string]any{}
	if strings.TrimSpace(value) != "" {
		if err := json.Unmarshal([]byte(value), &settings); err != nil || settings == nil {
			return "", false
		}
	}
	permissions, ok := settings["permissions"].(map[string]any)
	if !ok {
		if _, present := settings["permissions"]; present {
			return "", false
		}
		permissions = map[string]any{}
		settings["permissions"] = permissions
	}
	var deny []any
	if existing, present := permissions["deny"]; present {
		list, ok := existing.([]any)
		if !ok {
			return "", false
		}
		deny = list
	}
	for _, rule := range claudeMarshalStateDenyRules() {
		if !slices.Contains(deny, any(rule)) {
			deny = append(deny, rule)
		}
	}
	permissions["deny"] = deny
	data, err := json.Marshal(settings)
	if err != nil {
		return "", false
	}
	return string(data), true
}

// denyMarshalStateToClaude adds the private-state deny rules to a Claude
// Marshal chat launch, whatever its execution rights: into the launch's own
// --settings JSON when it has one (the read-only confinement's), otherwise as
// a --settings of its own before the opening cue. A --settings naming a file
// is the person's and is left as it is; the protocol rule then holds alone.
func denyMarshalStateToClaude(args []string) []string {
	for i, a := range args {
		if a == "--" {
			break
		}
		value, inline := "", false
		switch {
		case a == "--settings" && i+1 < len(args):
			value = args[i+1]
		case strings.HasPrefix(a, "--settings="):
			value, inline = strings.TrimPrefix(a, "--settings="), true
		default:
			continue
		}
		merged, ok := claudeMarshalStateSettings(value)
		if !ok {
			return args
		}
		out := slices.Clone(args)
		if inline {
			out[i] = "--settings=" + merged
		} else {
			out[i+1] = merged
		}
		return out
	}
	settings, _ := claudeMarshalStateSettings("")
	at := len(args) - len(trailingMarshalCue("claude", args))
	if at > 0 && args[at-1] == "--" {
		at--
	}
	return slices.Concat(args[:at], []string{"--settings", settings}, args[at:])
}

// denyMarshalStateFiles adds the provider's private-state rules to a Marshal
// chat launch that the launch has not already received elsewhere: Claude's
// here; agy's come with its session settings (configureMarshalMCP) and
// Codex's with its read-only permission profile (confineMarshalChat).
func denyMarshalStateFiles(provider string, args []string) []string {
	if canonicalNeutralProvider(provider) == "claude" {
		return denyMarshalStateToClaude(args)
	}
	return args
}
