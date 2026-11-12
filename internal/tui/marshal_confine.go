package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/statepath"
)

// The Marshal plans; workers change the project. With the default execution
// rights (read-only, or none) the Marshal chat's CLI is started so that it
// cannot write project files itself, where the CLI offers a way to say so:
//
//   - Codex: a MARSHAL permission profile (default_permissions) that makes
//     the project read-only except the two directories the protocol writes,
//     .marshal/marshal (plan pack and task list) and .marshal/proposals.
//     It covers shell commands and Codex's own file edits (apply_patch).
//   - Claude Code: see claudeMarshalConfineFlags (per edition).
//   - OpenCode and Antigravity have no per-launch, path-scoped write limit
//     (OpenCode's --help has none; Antigravity's --mode plan also refuses the
//     plan files), so only the protocol rule and the plan-only guard
//     (board_guard.go) hold there.
//
// With execution rights small-tasks the owner lets the Marshal do small work
// itself, and nothing is added.

// marshalWritableDirs are the directories under .marshal/ the Marshal chat
// writes: the plan pack and task list, and its proposal files.
var marshalWritableDirs = []string{"marshal", "proposals"}

// codexMarshalProfile names MARSHAL's Codex permission profile for the
// Marshal chat.
const codexMarshalProfile = "marshal_planner"

// marshalWriteMode is what stops the Marshal chat writing project files.
type marshalWriteMode uint8

const (
	// marshalWritesUnknown: no Marshal chat was launched by this workspace.
	marshalWritesUnknown marshalWriteMode = iota
	// marshalWritesBlocked: its CLI refuses project writes.
	marshalWritesBlocked
	// marshalWritesUnblocked: only the protocol stops it.
	marshalWritesUnblocked
	// marshalWritesAllowed: the owner gave it small-tasks rights.
	marshalWritesAllowed
)

// marshalChatConfined reports whether execution rights keep the Marshal from
// changing project files: every setting except small-tasks.
func marshalChatConfined(rights marshal.ExecutionRights) bool {
	return rights != marshal.RightsSmallTasks
}

// marshalWritesFor is what stops a Marshal chat on provider writing project
// files under rights.
func marshalWritesFor(provider string, rights marshal.ExecutionRights) marshalWriteMode {
	if !marshalChatConfined(rights) {
		return marshalWritesAllowed
	}
	switch canonicalNeutralProvider(provider) {
	case "codex":
		if codexMarshalConfineEnforced {
			return marshalWritesBlocked
		}
	case "claude":
		if _, enforced := claudeMarshalConfineFlags(); enforced {
			return marshalWritesBlocked
		}
	}
	return marshalWritesUnblocked
}

// confineMarshalChat adds the provider's read-only launch settings to a
// Marshal chat launch, unless rights let the Marshal do small tasks itself.
// A launch that already carries them (a rebuilt argv) is left as it is.
func confineMarshalChat(provider, root string, args []string, rights marshal.ExecutionRights) ([]string, error) {
	if !marshalChatConfined(rights) {
		return args, nil
	}
	switch canonicalNeutralProvider(provider) {
	case "codex":
		if codexConfigOverrides(args, "default_permissions") {
			return args, nil
		}
		if err := ensureMarshalWritableDirs(root); err != nil {
			return nil, err
		}
		return insertBeforeKickoff("codex", root, args, codexMarshalConfineFlags()), nil
	case "claude":
		flags, _ := claudeMarshalConfineFlags()
		if slices.Contains(args, flags[1]) && codexArgsHave(args, "--permission-mode") {
			return args, nil
		}
		// Every flag takes exactly one value, so the cue stays positional.
		// They go before the cue, and before a "--" that ends the options.
		at := len(args) - len(trailingMarshalCue("claude", args))
		if at > 0 && args[at-1] == "--" {
			at--
		}
		return slices.Concat(args[:at], flags, args[at:]), nil
	}
	return args, nil
}

// codexMarshalChatTips turns off Codex's own usage tips in the Marshal chat
// ("Tip: Press ^g to edit your current draft…" printed under an answer). It
// is a -c override for this launch only; the owner's config.toml is never
// edited. Codex 0.167 knows the key (`codex exec --strict-config -c
// tui.show_tooltips=false` loads, an unknown tui key is refused). Codex has
// no key that hides or collapses MCP tool-call cells; MARSHAL shapes its
// tool results instead (internal/mcp marshalResultContent).
const codexMarshalChatTips = "tui.show_tooltips=false"

// marshalChatViewArgs adds the Codex display overrides of a Marshal chat
// launch, once: a rebuilt argv that carries them is left as it is.
func marshalChatViewArgs(provider, root string, args []string) []string {
	if canonicalNeutralProvider(provider) != "codex" || codexConfigOverrides(args, "tui.show_tooltips") {
		return args
	}
	return insertBeforeKickoff("codex", root, args, []string{"-c", codexMarshalChatTips})
}

// codexMarshalConfineFlags are the -c overrides of MARSHAL's Codex
// permission profile: the whole disk readable, the project read-only, and
// only .marshal/marshal and .marshal/proposals writable. They override the
// operator's own sandbox_mode and default_permissions for this launch only.
func codexMarshalConfineFlags() []string {
	var writable []string
	for _, d := range marshalWritableDirs {
		writable = append(writable, fmt.Sprintf("%q=\"write\"", statepath.DirName+"/"+d))
	}
	filesystem := `{":root"="read", ":workspace_roots"={"."="read", ` + strings.Join(writable, ", ") + `}}`
	return []string{
		"-c", fmt.Sprintf("default_permissions=%q", codexMarshalProfile),
		"-c", "permissions." + codexMarshalProfile + ".filesystem=" + filesystem,
	}
}

// claudeMarshalWritableRules are Claude Code permission rules for the
// Marshal's own directories, relative to the project (the launch's working
// directory). Edit rules cover every Claude file-editing tool.
func claudeMarshalWritableRules() []string {
	var rules []string
	for _, d := range marshalWritableDirs {
		rules = append(rules, "Edit(./"+statepath.DirName+"/"+d+"/**)")
	}
	return rules
}

// claudeMarshalWritableSettings is a Claude Code --settings value that
// allows exactly claudeMarshalWritableRules.
func claudeMarshalWritableSettings() string {
	data, _ := json.Marshal(map[string]any{"permissions": map[string]any{"allow": claudeMarshalWritableRules()}})
	return string(data)
}

// ensureMarshalWritableDirs creates the Marshal's writable directories
// before launch: a sandbox can only open a directory that exists, and the
// Marshal cannot create one inside the read-only project.
func ensureMarshalWritableDirs(root string) error {
	for _, d := range marshalWritableDirs {
		path := statepath.Path(root, d)
		if err := statepath.CheckPath(root, path); err != nil {
			return err
		}
		if err := os.MkdirAll(path, 0o700); err != nil {
			return err
		}
	}
	return nil
}

// marshalExecutionRights are the project's execution rights for the Marshal;
// the edition default when no project store is attached, and read-only when
// the setting cannot be read.
func (w *Workspace) marshalExecutionRights(ctx context.Context) marshal.ExecutionRights {
	if w.runtime == nil {
		return marshal.DefaultSettings().ExecutionRights
	}
	service := w.runtime.Marshal()
	if service == nil || service.Store == nil {
		return marshal.DefaultSettings().ExecutionRights
	}
	record, err := service.Store.GetMarshalSettings(ctx, service.ProjectID)
	if err != nil {
		return marshal.RightsReadOnly
	}
	return record.Value.ExecutionRights
}

// confineWorkspaceMarshalChat confines one Marshal chat launch by the
// project's execution rights and remembers what then stops it writing, for
// the plan-only guard.
func (w *Workspace) confineWorkspaceMarshalChat(ctx context.Context, provider, root string, args []string) ([]string, error) {
	rights := w.marshalExecutionRights(ctx)
	args, err := confineMarshalChat(provider, root, args, rights)
	if err != nil {
		return nil, err
	}
	w.noteMarshalWrites(provider, rights)
	return args, nil
}

// noteMarshalWrites remembers, for the plan-only guard, what stops the
// Marshal chat just launched writing project files.
func (w *Workspace) noteMarshalWrites(provider string, rights marshal.ExecutionRights) {
	w.guard.mu.Lock()
	w.guard.marshalWrites = marshalWritesFor(provider, rights)
	w.guard.mu.Unlock()
}

func codexArgsHave(args []string, names ...string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		for _, n := range names {
			if a == n || strings.HasPrefix(a, n+"=") {
				return true
			}
		}
	}
	return false
}

func codexConfigOverrides(args []string, key string) bool {
	for i, a := range args {
		value := ""
		switch {
		case (a == "-c" || a == "--config") && i+1 < len(args):
			value = args[i+1]
		case strings.HasPrefix(a, "--config="):
			value = strings.TrimPrefix(a, "--config=")
		default:
			continue
		}
		k, _, _ := strings.Cut(value, "=")
		if strings.TrimSpace(k) == key {
			return true
		}
	}
	return false
}
