package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/permission"
	"github.com/Zen1th53/marshal/internal/sandbox"
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
//   - Other providers require supported OS isolation before execution.
//     When that is unavailable, project-bound launches are refused.
//
// With execution rights small-tasks the owner lets the Marshal do small work
// itself, and nothing is added.
//
// Reading MARSHAL's private state (its state database and the files around
// it) is a separate limit that holds whatever the rights: see
// marshal_state_guard.go.

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
		if codexArgsHave(args, "--dangerously-bypass-approvals-and-sandbox", "--yolo") {
			return nil, errors.New("read-only chat refuses sandbox bypass flags")
		}
		if codexConfigOverrides(args, "default_permissions") {
			flags := codexMarshalConfineFlags()
			if !marshalConfigMatches(args, "default_permissions", strings.TrimPrefix(flags[1], "default_permissions=")) || !marshalConfigMatches(args, "permissions."+codexMarshalProfile+".filesystem", strings.TrimPrefix(flags[3], "permissions."+codexMarshalProfile+".filesystem=")) {
				return nil, errors.New("read-only chat requires its verified filesystem profile")
			}
			return args, nil
		}
		if err := ensureMarshalWritableDirs(root); err != nil {
			return nil, err
		}
		return insertBeforeKickoff("codex", root, args, codexMarshalConfineFlags()), nil
	case "claude":
		for i, arg := range args {
			if arg == "--" {
				break
			}
			if arg == "--tools" {
				if i+1 >= len(args) || args[i+1] != "Read,Glob,Grep" || (i+2 < len(args) && !strings.HasPrefix(args[i+2], "-")) {
					return nil, errors.New("read-only chat refuses executable or editing built-in tools")
				}
			}
			if strings.HasPrefix(arg, "--tools=") && arg != "--tools=Read,Glob,Grep" {
				return nil, errors.New("read-only chat refuses executable or editing built-in tools")
			}
		}
		flags, _ := claudeMarshalConfineFlags()
		if slices.Contains(args, flags[1]) && codexArgsHave(args, "--permission-mode") && slices.Contains(args, "Read,Glob,Grep") && slices.Contains(args, "--tools") && slices.Contains(args, "--restricted") {
			return args, nil
		}
		// Every flag takes exactly one value, so the cue stays positional.
		// They go before the cue, and before a "--" that ends the options.
		at := len(args) - len(trailingMarshalCue("claude", args))
		if at > 0 && args[at-1] == "--" {
			at--
		}
		// Built-in shell and edit tools cannot be restored by inherited allow
		// rules or a provider approval prompt. Runtime MCP tools retain their
		// own policy checks and operator grants.
		flags = append(flags, "--restricted", "--tools", "Read,Glob,Grep")
		if at < len(args) && args[at] != "--" {
			flags = append(flags, "--")
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
// permission profile: the whole disk readable, the project read-only, only
// .marshal/marshal and .marshal/proposals writable, and MARSHAL's private
// state (marshalPrivateStatePaths) neither readable nor writable. They
// override the operator's own sandbox_mode and default_permissions for this
// launch only. Codex 0.169 applies a "none" entry to every shell command of
// the session (`codex sandbox` with this profile: cat, sqlite3 and python of
// .marshal/state.db fail with "Operation not permitted"); its MCP servers run
// outside the sandbox, so MARSHAL's own tools keep their access.
func codexMarshalConfineFlags() []string {
	var writable []string
	for _, d := range marshalWritableDirs {
		writable = append(writable, fmt.Sprintf("%q=\"write\"", statepath.DirName+"/"+d))
	}
	entries := append(writable, codexMarshalStateFilesystem()...)
	filesystem := `{":root"="read", ":workspace_roots"={"."="read", ` + strings.Join(entries, ", ") + `}}`
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
	decision := w.enforceMarshalLaunchConfinement(ctx, surfaceWorkspaceNativeLaunch, provider, root, rights)
	if !decision.Allowed || decision.ToolOnlyFallback {
		return nil, errors.New(decision.Reason)
	}
	return args, nil
}

// noteMarshalWrites remembers, for the plan-only guard, what stops the
// Marshal chat just launched writing project files.
func (w *Workspace) noteMarshalWrites(provider string, rights marshal.ExecutionRights) {
	w.guard.mu.Lock()
	w.guard.marshalWrites = marshalWritesFor(provider, rights)
	w.guard.mu.Unlock()
}

// marshalBoundarySurface identifies each launch, execution, apply, permission
// and Git boundary enforced outside an active run.
type marshalBoundarySurface string

const (
	surfaceWorkspaceNativeLaunch   marshalBoundarySurface = "workspace-native-launch"
	surfaceWorkspaceNativeReattach marshalBoundarySurface = "workspace-native-reattach"
	surfaceTmuxExecution           marshalBoundarySurface = "tmux-execution"
	surfaceCLIHeadlessStart        marshalBoundarySurface = "cli-headless-start"
	surfaceFileMCPApply            marshalBoundarySurface = "file-mcp-apply"
	surfacePermissionAdapter       marshalBoundarySurface = "permission-adapter"
	surfaceCheckoutWorktree        marshalBoundarySurface = "checkout-worktree"
	surfaceRuntimeGitWriter        marshalBoundarySurface = "runtime-git-writer"
)

// allMarshalBoundarySurfaces lists every boundary enforced across editions and
// platforms.
var allMarshalBoundarySurfaces = []marshalBoundarySurface{
	surfaceWorkspaceNativeLaunch,
	surfaceWorkspaceNativeReattach,
	surfaceTmuxExecution,
	surfaceCLIHeadlessStart,
	surfaceFileMCPApply,
	surfacePermissionAdapter,
	surfaceCheckoutWorktree,
	surfaceRuntimeGitWriter,
}

// marshalConfinementMechanism records how a boundary enforces out-of-run
// protection.
type marshalConfinementMechanism string

const (
	confinementOSBwrap          marshalConfinementMechanism = "os-bwrap-readonly"
	confinementProviderVerified marshalConfinementMechanism = "provider-verified-readonly"
	confinementGovernedOperator marshalConfinementMechanism = "governed-operator-gate"
	confinementSmallTasksScoped marshalConfinementMechanism = "small-tasks-scoped-worktree"
	confinementToolOnlyRead     marshalConfinementMechanism = "tool-only-read"
	confinementRefused          marshalConfinementMechanism = "refused"
)

// marshalConfinementRequest describes one out-of-run launch, permission or
// writer decision to evaluate.
type marshalConfinementRequest struct {
	Edition               string
	Platform              string
	OSSandboxAvailable    *bool
	Surface               marshalBoundarySurface
	Provider              string
	Rights                marshal.ExecutionRights
	InRun                 bool
	ExplicitOperatorGrant bool
	ScopedWorktreeAuth    bool
	ToolName              string
	Command               string
	TargetPath            string
}

// marshalConfinementDecision records the evaluated boundary policy for one
// surface, edition, platform and provider.
type marshalConfinementDecision struct {
	Edition                 string                      `json:"edition"`
	Platform                string                      `json:"platform"`
	Surface                 marshalBoundarySurface      `json:"surface"`
	Provider                string                      `json:"provider,omitempty"`
	Rights                  marshal.ExecutionRights     `json:"rights"`
	Mechanism               marshalConfinementMechanism `json:"mechanism"`
	Allowed                 bool                        `json:"allowed"`
	AllowProjectExec        bool                        `json:"allow_project_exec"`
	AllowProjectWrite       bool                        `json:"allow_project_write"`
	AllowGitWrite           bool                        `json:"allow_git_write"`
	ProtectsWorktreeAndGit  bool                        `json:"protects_worktree_and_git"`
	ToolOnlyFallback        bool                        `json:"tool_only_fallback"`
	RequiresOperatorGrant   bool                        `json:"requires_operator_grant"`
	RequiresScopedWorktree  bool                        `json:"requires_scoped_worktree"`
	GitDetectionIndependent bool                        `json:"git_detection_independent"`
	Reason                  string                      `json:"reason"`
}

func currentMarshalEdition() string {
	if editionExtreme {
		return "default"
	}
	return "governed"
}

// probeMarshalOSSandbox checks whether supported Linux OS isolation (bwrap)
// is available on this host.
func probeMarshalOSSandbox(ctx context.Context) (string, bool) {
	if runtime.GOOS != "linux" {
		return "", false
	}
	bin, err := exec.LookPath("bwrap")
	if err != nil || bin == "" {
		return "", false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	cap := sandbox.NewBwrap(bin).Probe(ctx)
	if !cap.Available {
		return "", false
	}
	return bin, true
}

// wrapMarshalReadOnlySandbox wraps command in Linux bubblewrap with the
// project worktree and its Git metadata mounted read-only.
func wrapMarshalReadOnlySandbox(ctx context.Context, root string, command []string) (model.CommandSpec, error) {
	bin, ok := probeMarshalOSSandbox(ctx)
	if !ok {
		return model.CommandSpec{}, errors.New("Linux bubblewrap isolation is unavailable")
	}
	return sandbox.NewBwrap(bin).Wrap(model.SandboxRequest{
		Worktree:         root,
		ReadOnlyWorktree: true,
	}, command)
}

func providerHasVerifiedReadOnlyConfinement(edition, provider string) bool {
	switch canonicalNeutralProvider(provider) {
	case "codex", "claude":
		return edition == "default" || edition == "governed"
	default:
		return false
	}
}

func isMarshalWritableRelPath(path string) bool {
	p := filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
	p = strings.TrimPrefix(p, "./")
	if p == "" || p == "." || strings.HasPrefix(p, "/") || strings.HasPrefix(p, "../") || strings.Contains(p, "/../") {
		return false
	}
	if p == ".git" || strings.HasPrefix(p, ".git/") || strings.Contains(p, "/.git/") || strings.HasSuffix(p, "/.git") {
		return false
	}
	for _, d := range marshalWritableDirs {
		prefix := statepath.DirName + "/" + d
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			return true
		}
	}
	return false
}

// isGitWriteOrCommitTool reports whether a tool call attempts a Git commit or
// Git metadata/history mutation.
func isGitWriteOrCommitTool(toolName, command string) bool {
	tool := strings.ToLower(strings.TrimSpace(toolName))
	cmd := strings.ToLower(strings.TrimSpace(command))
	if cmd == "" {
		cmd = tool
	}
	if strings.Contains(cmd, ".git/") || strings.HasSuffix(cmd, "/.git") || cmd == ".git" {
		if tool == "write" || tool == "edit" || tool == "multiedit" || tool == "notebookedit" || tool == "apply_patch" || strings.Contains(cmd, "write") || strings.Contains(cmd, "rm ") || strings.Contains(cmd, "mv ") || strings.Contains(cmd, ">") {
			return true
		}
	}
	for _, sub := range []string{
		"git commit", "git add", "git push", "git reset", "git checkout",
		"git switch", "git merge", "git rebase", "git cherry-pick",
		"git revert", "git tag", "git branch -", "git stash", "git rm",
		"git mv", "git apply", "git am", "git update-ref", "git write-tree",
		"git commit-tree", "git init",
	} {
		if strings.Contains(cmd, sub) {
			return true
		}
	}
	// Match `git -C <dir> commit` and similar global flags before a mutating subcommand.
	fields := strings.Fields(cmd)
	for i := 0; i < len(fields); i++ {
		if fields[i] == "git" || strings.HasSuffix(fields[i], "/git") {
			for j := i + 1; j < len(fields); j++ {
				tok := fields[j]
				if tok == "commit" || tok == "add" || tok == "push" || tok == "reset" || tok == "checkout" || tok == "merge" || tok == "rebase" || tok == "apply" || tok == "commit-tree" || tok == "update-ref" {
					return true
				}
				if tok == ";" || tok == "&&" || tok == "||" || tok == "|" {
					break
				}
			}
		}
	}
	return false
}

// isProjectWriteTool reports whether a tool call mutates project files outside
// the Marshal's own planning directories.
func isProjectWriteTool(toolName, command, targetPath string) bool {
	tool := strings.ToLower(strings.TrimSpace(toolName))
	switch tool {
	case "write", "edit", "multiedit", "notebookedit", "apply_patch", "patch", "delete", "remove":
		target := strings.TrimSpace(targetPath)
		if target == "" {
			target = strings.TrimSpace(command)
		}
		return !isMarshalWritableRelPath(target)
	}
	return isGitWriteOrCommitTool(toolName, command)
}

// evaluateMarshalConfinement evaluates the edition and platform confinement
// boundary for any out-of-run launch, execution, apply, permission or Git
// writer surface.
func evaluateMarshalConfinement(req marshalConfinementRequest) marshalConfinementDecision {
	edition := strings.TrimSpace(req.Edition)
	if edition == "" {
		edition = currentMarshalEdition()
	}
	platform := strings.TrimSpace(req.Platform)
	if platform == "" {
		platform = runtime.GOOS
	}
	rights := req.Rights
	if rights == "" {
		rights = marshal.RightsReadOnly
	}
	provider := canonicalNeutralProvider(req.Provider)
	osSandbox := false
	if req.OSSandboxAvailable != nil {
		osSandbox = *req.OSSandboxAvailable && platform == "linux"
	} else if platform == "linux" {
		_, osSandbox = probeMarshalOSSandbox(context.Background())
	}

	d := marshalConfinementDecision{
		Edition:                 edition,
		Platform:                platform,
		Surface:                 req.Surface,
		Provider:                provider,
		Rights:                  rights,
		ProtectsWorktreeAndGit:  true,
		GitDetectionIndependent: true,
		RequiresOperatorGrant:   edition == "governed",
	}

	switch req.Surface {
	case surfaceRuntimeGitWriter:
		if !req.InRun {
			if isGitWriteOrCommitTool("git", req.Command) || strings.Contains(strings.ToLower(req.Command), "commit") || req.Command == "" {
				d.Mechanism = confinementRefused
				d.Allowed = false
				d.Reason = "out-of-run Git evidence commits are refused; persist evidence under state storage"
				return d
			}
			// Explicit owner-confirmed guard undo restores reported paths without committing.
			if req.ExplicitOperatorGrant && strings.Contains(strings.ToLower(req.Command), "undo") {
				d.Mechanism = confinementGovernedOperator
				d.Allowed = true
				d.RequiresOperatorGrant = true
				d.Reason = "owner-confirmed guard undo restores reported files without committing"
				return d
			}
			d.Mechanism = confinementRefused
			d.Allowed = false
			d.Reason = "unapproved out-of-run Git writer refused"
			return d
		}
		d.Mechanism = confinementSmallTasksScoped
		d.Allowed = true
		d.AllowGitWrite = true
		d.Reason = "active run Git writer"
		return d

	case surfaceCheckoutWorktree:
		if !req.InRun {
			if rights == marshal.RightsSmallTasks && req.ScopedWorktreeAuth && (edition != "governed" || req.ExplicitOperatorGrant) {
				d.Mechanism = confinementSmallTasksScoped
				d.Allowed = true
				d.AllowProjectWrite = true
				d.RequiresScopedWorktree = true
				d.Reason = "small-tasks authorized scoped runtime worktree"
				return d
			}
			d.Mechanism = confinementRefused
			d.Allowed = false
			d.RequiresScopedWorktree = rights == marshal.RightsSmallTasks
			d.Reason = "out-of-run checkout or worktree mutation requires authorized scoped worktree"
			return d
		}
		d.Mechanism = confinementSmallTasksScoped
		d.Allowed = true
		d.AllowProjectWrite = true
		d.Reason = "active run worktree"
		return d

	case surfaceFileMCPApply:
		if !req.InRun {
			if req.TargetPath != "" && isMarshalWritableRelPath(req.TargetPath) && !isGitWriteOrCommitTool(req.ToolName, req.Command) {
				d.Mechanism = confinementProviderVerified
				if edition == "governed" {
					d.Mechanism = confinementGovernedOperator
				}
				d.Allowed = true
				d.Reason = "planning write confined to Marshal planning directories"
				return d
			}
			if rights == marshal.RightsSmallTasks && req.ScopedWorktreeAuth && (edition != "governed" || req.ExplicitOperatorGrant) && !isGitWriteOrCommitTool(req.ToolName, req.Command) {
				d.Mechanism = confinementSmallTasksScoped
				d.Allowed = true
				d.AllowProjectWrite = true
				d.RequiresScopedWorktree = true
				d.Reason = "small-tasks file/MCP apply inside explicitly authorized scoped worktree"
				return d
			}
			d.Mechanism = confinementRefused
			d.Allowed = false
			d.RequiresScopedWorktree = rights == marshal.RightsSmallTasks
			d.Reason = "unapproved out-of-run file or MCP project write refused"
			return d
		}
		d.Mechanism = confinementSmallTasksScoped
		d.Allowed = true
		d.AllowProjectWrite = true
		d.Reason = "active run file/MCP apply"
		return d

	case surfacePermissionAdapter:
		if !req.InRun {
			if isGitWriteOrCommitTool(req.ToolName, req.Command) {
				if rights != marshal.RightsSmallTasks {
					d.Mechanism = confinementRefused
					d.Allowed = false
					d.Reason = "read-only Marshal chat cannot commit or modify Git metadata outside a run"
					return d
				}
				if !req.ScopedWorktreeAuth || (edition == "governed" && !req.ExplicitOperatorGrant) {
					d.Mechanism = confinementRefused
					d.Allowed = false
					d.RequiresScopedWorktree = true
					d.Reason = "small-tasks Git write requires explicit scoped worktree authorization"
					return d
				}
			}
			if isProjectWriteTool(req.ToolName, req.Command, req.TargetPath) {
				if rights != marshal.RightsSmallTasks {
					if edition == "governed" && req.ExplicitOperatorGrant {
						d.Mechanism = confinementGovernedOperator
						d.Allowed = true
						d.AllowProjectWrite = true
						d.RequiresOperatorGrant = true
						d.Reason = "governed operator grant preserved for explicit non-Git project edit"
						return d
					}
					d.Mechanism = confinementRefused
					d.Allowed = false
					d.Reason = "read-only Marshal chat cannot modify project files outside a run"
					return d
				}
				if !req.ScopedWorktreeAuth || (edition == "governed" && !req.ExplicitOperatorGrant) {
					d.Mechanism = confinementRefused
					d.Allowed = false
					d.RequiresScopedWorktree = true
					d.Reason = "small-tasks project write requires explicitly authorized scoped worktree"
					return d
				}
				d.Mechanism = confinementSmallTasksScoped
				d.Allowed = true
				d.AllowProjectWrite = true
				d.RequiresScopedWorktree = true
				d.Reason = "small-tasks project write inside explicitly authorized scoped worktree"
				return d
			}
			if edition == "governed" {
				d.Mechanism = confinementGovernedOperator
				d.Allowed = req.ExplicitOperatorGrant
				d.RequiresOperatorGrant = true
				if d.Allowed {
					d.Reason = "governed operator grant allowed non-mutating tool call"
				} else {
					d.Reason = "governed tool call awaits operator grant"
				}
				return d
			}
			d.Mechanism = confinementProviderVerified
			d.Allowed = true
			d.Reason = "non-mutating tool call allowed"
			return d
		}
		d.Mechanism = confinementGovernedOperator
		d.Allowed = true
		d.Reason = "active run permission decision"
		return d

	case surfaceWorkspaceNativeLaunch, surfaceWorkspaceNativeReattach, surfaceTmuxExecution, surfaceCLIHeadlessStart:
		if rights == marshal.RightsSmallTasks {
			if req.ScopedWorktreeAuth && (edition != "governed" || req.ExplicitOperatorGrant) {
				d.Mechanism = confinementSmallTasksScoped
				d.Allowed = true
				d.AllowProjectExec = true
				d.AllowProjectWrite = true
				d.RequiresScopedWorktree = true
				d.Reason = "small-tasks launch uses explicitly authorized scoped runtime worktree"
				return d
			}
			d.Mechanism = confinementRefused
			d.Allowed = false
			d.RequiresScopedWorktree = true
			d.Reason = "small-tasks launch requires explicitly authorized scoped runtime worktree"
			return d
		}
		if osSandbox && !providerHasVerifiedReadOnlyConfinement(edition, provider) {
			d.Mechanism = confinementOSBwrap
			d.Allowed = true
			d.Reason = "Linux bubblewrap read-only sandbox protects worktree and Git metadata"
			return d
		}
		if providerHasVerifiedReadOnlyConfinement(edition, provider) {
			if edition == "governed" {
				d.Mechanism = confinementGovernedOperator
				d.Allowed = true
				d.RequiresOperatorGrant = true
				d.Reason = "governed provider confinement preserves operator grants and gates"
				return d
			}
			d.Mechanism = confinementProviderVerified
			d.Allowed = true
			d.Reason = "verified provider read-only confinement protects worktree and Git metadata"
			return d
		}
		d.Mechanism = confinementToolOnlyRead
		d.Allowed = true
		d.ToolOnlyFallback = true
		d.Reason = "OS isolation and proved provider confinement unavailable; project-bound exec and writes refused, falling back to scoped tool-only read"
		return d
	}

	d.Mechanism = confinementRefused
	d.Allowed = false
	d.Reason = "unknown confinement surface"
	return d
}

// marshalBoundaryMatrix records the complete edition and platform enforcement
// matrix across every out-of-run launch, writer, permission and Git boundary.
func marshalBoundaryMatrix() []marshalConfinementDecision {
	bTrue := true
	bFalse := false
	platforms := []struct {
		os    string
		bwrap *bool
	}{
		{"linux", &bTrue},
		{"linux", &bFalse},
		{"darwin", &bFalse},
	}
	editions := []string{"default", "governed"}
	providers := []string{"codex", "claude", "opencode", "antigravity"}
	rightsList := []marshal.ExecutionRights{marshal.RightsReadOnly, marshal.RightsSmallTasks}

	var rows []marshalConfinementDecision
	for _, ed := range editions {
		for _, pl := range platforms {
			for _, surface := range allMarshalBoundarySurfaces {
				switch surface {
				case surfaceWorkspaceNativeLaunch, surfaceWorkspaceNativeReattach, surfaceTmuxExecution, surfaceCLIHeadlessStart:
					for _, provider := range providers {
						for _, rights := range rightsList {
							rows = append(rows, evaluateMarshalConfinement(marshalConfinementRequest{
								Edition:            ed,
								Platform:           pl.os,
								OSSandboxAvailable: pl.bwrap,
								Surface:            surface,
								Provider:           provider,
								Rights:             rights,
							}))
						}
					}
				case surfaceRuntimeGitWriter:
					rows = append(rows, evaluateMarshalConfinement(marshalConfinementRequest{
						Edition:            ed,
						Platform:           pl.os,
						OSSandboxAvailable: pl.bwrap,
						Surface:            surface,
						Rights:             marshal.RightsReadOnly,
						Command:            "git commit -m evidence",
					}))
				case surfaceCheckoutWorktree:
					for _, rights := range rightsList {
						rows = append(rows, evaluateMarshalConfinement(marshalConfinementRequest{
							Edition:            ed,
							Platform:           pl.os,
							OSSandboxAvailable: pl.bwrap,
							Surface:            surface,
							Rights:             rights,
						}))
					}
				case surfaceFileMCPApply:
					for _, rights := range rightsList {
						rows = append(rows, evaluateMarshalConfinement(marshalConfinementRequest{
							Edition:            ed,
							Platform:           pl.os,
							OSSandboxAvailable: pl.bwrap,
							Surface:            surface,
							Rights:             rights,
							ToolName:           "Write",
							TargetPath:         "main.go",
						}))
					}
				case surfacePermissionAdapter:
					rows = append(rows, evaluateMarshalConfinement(marshalConfinementRequest{
						Edition:               ed,
						Platform:              pl.os,
						OSSandboxAvailable:    pl.bwrap,
						Surface:               surface,
						Rights:                marshal.RightsReadOnly,
						ExplicitOperatorGrant: true,
						ToolName:              "Bash",
						Command:               "git commit -am out-of-run",
					}))
				}
			}
		}
	}
	return rows
}

// marshalConfinementEnv encodes the evaluated confinement policy into child
// process environment variables without altering provider CLI argv shapes.
func marshalConfinementEnv(d marshalConfinementDecision) []string {
	boolVal := func(v bool) string {
		if v {
			return "1"
		}
		return "0"
	}
	return []string{
		"MARSHAL_CONFINEMENT_MODE=" + string(d.Mechanism),
		"MARSHAL_ALLOW_PROJECT_EXEC=" + boolVal(d.AllowProjectExec),
		"MARSHAL_ALLOW_PROJECT_WRITE=" + boolVal(d.AllowProjectWrite),
		"MARSHAL_ALLOW_GIT_WRITE=" + boolVal(d.AllowGitWrite),
	}
}

func (w *Workspace) activeMarshalRunID() string {
	m := w.marshalSession()
	if m == nil {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.runID
}

func (w *Workspace) isMarshalRunExecuting() bool {
	w.mu.RLock()
	p := w.state.Marshal
	w.mu.RUnlock()
	return p != nil && slices.Contains(guardRunActive, p.Board.Run.State)
}

// enforceMarshalLaunchConfinement evaluates and records the confinement policy
// for a workspace launch or reattach and returns its boundary decision.
func (w *Workspace) enforceMarshalLaunchConfinement(ctx context.Context, surface marshalBoundarySurface, provider, root string, rights marshal.ExecutionRights) marshalConfinementDecision {
	decision := evaluateMarshalConfinement(marshalConfinementRequest{
		Surface:  surface,
		Provider: provider,
		Rights:   rights,
		InRun:    w.isMarshalRunExecuting(),
	})
	w.guard.mu.Lock()
	w.guard.confinement = decision
	w.guard.mu.Unlock()
	if decision.ToolOnlyFallback && root != "" {
		w.RecordActivity(fmt.Sprintf("%s chat confinement fallback: scoped tool-only read (project-bound exec and writes refused).", providerDisplayName(provider)))
	}
	return decision
}

func isExplicitOperatorPermissionSource(source string) bool {
	s := strings.ToLower(strings.TrimSpace(source))
	if s == "" || s == "marshal" || strings.HasPrefix(s, strings.ToLower(runGrantSource)) {
		return false
	}
	return strings.HasPrefix(s, "operator ")
}

// evaluatePermissionToolBoundary enforces out-of-run confinement at the
// permission adapter boundary (decidePermission).
func (w *Workspace) evaluatePermissionToolBoundary(ctx context.Context, req permission.Request, source string) marshalConfinementDecision {
	rights := w.marshalExecutionRights(ctx)
	toolName, command := splitPermissionToolObject(req.Object)
	inRun := w.isMarshalRunExecuting()
	explicitGrant := isExplicitOperatorPermissionSource(source) || (req.RunID != "" && strings.HasPrefix(source, runGrantSource))
	return evaluateMarshalConfinement(marshalConfinementRequest{
		Surface:               surfacePermissionAdapter,
		Rights:                rights,
		InRun:                 inRun,
		ExplicitOperatorGrant: explicitGrant,
		ToolName:              toolName,
		Command:               command,
		TargetPath:            command,
	})
}

func splitPermissionToolObject(object string) (toolName, rest string) {
	object = strings.TrimSpace(object)
	if object == "" {
		return "", ""
	}
	toolName, rest, _ = strings.Cut(object, " ")
	return strings.TrimSpace(toolName), strings.TrimSpace(rest)
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

// confineMarshalCommand applies the selected boundary to the actual executable.
// Environment hints alone never establish isolation.
func confineMarshalCommand(ctx context.Context, provider, root string, rights marshal.ExecutionRights, command []string) ([]string, error) {
	d := evaluateMarshalConfinement(marshalConfinementRequest{Surface: surfaceTmuxExecution, Provider: provider, Rights: rights})
	if !d.Allowed || d.ToolOnlyFallback {
		return nil, errors.New(d.Reason)
	}
	if d.Mechanism == confinementOSBwrap {
		spec, err := wrapMarshalReadOnlySandbox(ctx, root, command)
		if err != nil {
			return nil, err
		}
		return append([]string{spec.Path}, spec.Args...), nil
	}
	return command, nil
}

func marshalConfigMatches(args []string, key, expected string) bool {
	found := false
	for i, a := range args {
		if a == "--" {
			break
		}
		value := ""
		if (a == "-c" || a == "--config") && i+1 < len(args) {
			value = args[i+1]
		}
		if strings.HasPrefix(a, "--config=") {
			value = strings.TrimPrefix(a, "--config=")
		}
		k, v, ok := strings.Cut(value, "=")
		if ok && strings.TrimSpace(k) == key {
			if strings.TrimSpace(v) != expected {
				return false
			}
			found = true
		}
	}
	return found
}
