package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Zen1th53/marshal/internal/a2a"
	"github.com/Zen1th53/marshal/internal/api"
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/doctor"
	"github.com/Zen1th53/marshal/internal/httpsrv"
	"github.com/Zen1th53/marshal/internal/legal"
	"github.com/Zen1th53/marshal/internal/mcp"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/policytest"
	"github.com/Zen1th53/marshal/internal/project"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/statepath"
	"github.com/Zen1th53/marshal/internal/store"
	"github.com/Zen1th53/marshal/internal/tui"
)

var (
	Version   = defaultVersion
	Commit    = defaultCommit
	BuildDate = defaultBuildDate
)

func init() {
	// The workspace reports and compares the same version the CLI does. It is
	// handed over rather than imported so the TUI keeps no dependency on the
	// CLI package.
	//
	// A source build carries only the defaults above, so identify it from the
	// VCS stamp Go embeds when present. Values injected via -ldflags always
	// win, and the defaults are kept when no stamp exists.
	Version, Commit, BuildDate = fillBuildIdentity(Version, Commit, BuildDate, currentVCSStamp())
	tui.BuildVersion = Version
}

const usage = `Usage: marshal [--json] <command> [arguments]

Commands:
  telemetry status | disable | enable
  ultra request
  init
  doctor [--probe-providers] [--deep]
  egm status [--provider X] | install --provider X | record --stage X --provider X --egm-version X --file PATH
  status
  agent register --name NAME --role ROLE [--provider PROVIDER] [--model MODEL]
  agents
  tasks
  task import tasks.json [--dry-run]
  task show TASK-ID
  task claim TASK-ID --agent AGENT-ID [--revision N]
  task release TASK-ID
  run TASK-ID --adapter ADAPTER [--model MODEL] [--agent AGENT-ID]
  logs TASK-ID
  cancel TASK-ID
  adapters
  adapter probe NAME
  mcp serve [--listen ADDR] | mcp status
  a2a serve [--listen ADDR] | a2a status
  events
  artifacts
  verify [-- command args...]
  reconcile --file-state state.json
  memory status | recall | show | list | candidates | promote | tombstone | audit | remember | write
  permission read <allow|deny|revoke|list> <absolute folder>
  policy test SUITE-FILE
  legal audit [--json] | legal export --output PATH
  setup | setup status
  update [install]
  goal <request> | goal explain <request>
  architecture serve [--proposal FILE] [--layout FILE] [--provider PROVIDER] [--no-open]
  architecture draft [--request REQUEST] [--output FILE] [--provider PROVIDER]
  architecture feedback [--provider PROVIDER]
  plan create SESSION-ID --file INPUT.json | show PROJECT-ID | approve PROJECT-ID | cancel PROJECT-ID | handoff SESSION-ID PROJECT-ID
  exec start --session SESSION-ID --project PROJECT-ID | run RUN-ID | status RUN-ID | approve APPROVAL-ID | rollback CHECKPOINT-ID | handoff RUN-ID
  review start SESSION.json | status VERIFICATION-ID | evaluate VERIFICATION-ID | attest VERIFICATION-ID --bundle ENVELOPE.json --provenance TEXT
  learning commit INPUT.json | show MEMORY-COMMIT-ID | item ITEM-ID | history ITEM-ID | search --project ID | context --project ID | invalidate INPUT.json | trust [TASK-CLASS] | fingerprints --project ID | playbooks --project ID | replays [RUN-ID] | benchmarks [NAME] | export --project ID | restore BUNDLE.json --project ID
  optimization start INPUT.json | show CYCLE-ID | candidates CYCLE-ID | counterfactuals CYCLE-ID | manifests CYCLE-ID | canaries CYCLE-ID
  help [TOPIC] | help why
  constitution version | invariants | decisions SESSION-ID | violations SESSION-ID
  tui [SESSION-ID]
  codex [NATIVE-CODEX-ARGUMENTS...]
  claude [NATIVE-CLAUDE-ARGUMENTS...]
  opencode [NATIVE-OPENCODE-ARGUMENTS...]
  agy [NATIVE-AGY-ARGUMENTS...]      (also: antigravity)
  auth token <create|list|revoke>
  gc worktrees | artifacts
  state backup | verify-backup | restore
  architecture receipt import FILE
  daemon
  version
`

const policyTestUsage = "Usage: marshal policy test SUITE-FILE\n"
const telemetryChildFlag = "--marshal-internal-telemetry-child"

type command struct {
	root   string
	json   bool
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
	// stopTelemetry ends this process's telemetry; a session relaunch
	// calls it before the exec that skips the deferred call.
	stopTelemetry func()
	// startedRepository is the "Started a new Git repository …" line this
	// launch printed, for the control centre's first Activity line: the
	// terminal line is covered by the TUI as soon as it opens.
	startedRepository string
}

func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return Execute(ctx, root, args, stdin, stdout, stderr)
}

func Execute(ctx context.Context, root string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	ctx = withWorkerHost(ctx)
	c := command{root: root, stdin: stdin, stdout: stdout, stderr: stderr}
	if dispatcherHelp(args, stdout) {
		return 0
	}
	args, c.json = globalJSON(args)
	if usesProject(args) {
		if err := statepath.ValidateRoot(root); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if showsHostNotice(args) {
			if err := c.hostWorkerNotice(); err != nil {
				fmt.Fprintln(stderr, err)
				return 1
			}
		}
	}
	ctx, stopTelemetry := c.startTelemetry(ctx, args)
	c.stopTelemetry = sync.OnceFunc(stopTelemetry)
	defer c.stopTelemetry()
	if len(args) > 1 && args[0] == "tui" && args[1] == telemetryChildFlag {
		args = append(args[:1], args[2:]...)
	}
	if len(args) == 0 {
		// No arguments follows the same validated startup path as `marshal tui`.
		if err := c.relaunchSession(c.controlCenter(ctx, nil), nil); err != nil {
			fmt.Fprintln(stderr, err)
			return exitCode(err)
		}
		return 0
	}
	var err error
	switch args[0] {
	case "telemetry":
		err = c.telemetry(ctx, args[1:])
	case "ultra":
		err = c.ultra(ctx, args[1:])
	case "version", "--version", "-version", "-v":
		versionLine := fmt.Sprintf("Marshal %s (commit: %s, build date: %s, schema: v%d, constitution: %s)",
			Version, Commit, BuildDate, store.LatestSchemaVersion, constitutionVersionString())
		versionData := map[string]any{
			"version":              Version,
			"commit":               Commit,
			"build_date":           BuildDate,
			"schema_version":       store.LatestSchemaVersion,
			"constitution_version": constitutionVersionString(),
		}
		if tui.Edition() == "Marshal" {
			versionData["edition"] = "Marshal"
		}
		err = c.print(versionData, versionLine)
	case "update":
		err = c.update(ctx, args[1:])
	case "init":
		err = c.init(ctx)
	case "doctor":
		return c.doctor(ctx, args[1:])
	case "daemon":
		err = c.daemon(ctx)
	case "status":
		err = c.status(ctx)
	case "agent":
		err = c.agent(ctx, args[1:])
	case "agents":
		err = c.agents(ctx)
	case "tasks":
		err = c.tasks(ctx)
	case "task":
		err = c.task(ctx, args[1:])
	case "run":
		err = c.run(ctx, args[1:])
	case "logs":
		err = c.logs(ctx, args[1:])
	case "cancel":
		err = c.cancel(ctx, args[1:])
	case "events":
		err = c.events(ctx)
	case "artifacts":
		err = c.artifacts(ctx)
	case "verify":
		err = c.verify(ctx, args[1:])
	case "reconcile":
		err = c.reconcile(ctx, args[1:])
	case "policy":
		err = c.policy(ctx, args[1:])
	case "adapters":
		err = c.adapters(ctx)
	case "adapter":
		err = c.adapter(ctx, args[1:])
	case "mcp":
		err = c.mcp(ctx, args[1:])
	case "a2a":
		err = c.a2a(ctx, args[1:])
	case "auth":
		err = c.auth(ctx, args[1:])
	case "legal":
		err = c.legal(ctx, args[1:])
	case "gc":
		err = c.gc(ctx, args[1:])
	case "state":
		err = c.state(ctx, args[1:])
	case "architecture":
		if len(args) > 1 && args[1] == "receipt" {
			err = c.architectureReceipt(ctx, args[1:])
		} else {
			err = c.architecture(ctx, args[1:])
		}
	case "egm":
		err = c.egm(ctx, args[1:])
	case "memory":
		err = c.memory(ctx, args[1:])
	case "permission":
		err = c.permission(ctx, args[1:])
	case "setup":
		err = c.setup(ctx, args[1:])
	case "goal":
		err = c.goal(ctx, args[1:])
	case "plan":
		err = c.plan(ctx, args[1:])
	case "exec", "execution":
		err = c.exec(ctx, args[1:])
	case "review", "verification":
		err = c.review(ctx, args[1:])
	case "learning":
		err = c.learning(ctx, args[1:])
	case "optimization":
		err = c.optimization(ctx, args[1:])
	case "help":
		err = c.help(ctx, args[1:])
	case "constitution":
		err = c.constitution(ctx, args[1:])
	case "tui":
		err = c.relaunchSession(c.tui(ctx, args[1:]), args[1:])
	case "codex":
		return c.nativeProvider(ctx, "codex", args[1:])
	case "claude":
		return c.nativeProvider(ctx, "claude", args[1:])
	case "opencode":
		return c.nativeProvider(ctx, "opencode", args[1:])
	case "agy", "antigravity":
		return c.nativeProvider(ctx, "antigravity", args[1:])
	default:
		err = fmt.Errorf("%w: unknown command %s", model.ErrInvalid, args[0])
	}
	if err != nil {
		if c.json {
			_ = c.print(map[string]any{"error": err.Error(), "exit_code": exitCode(err)}, "")
		} else if errors.Is(err, api.ErrRuntimeNotRunning) && heldByWindow(root) {
			fmt.Fprintln(stderr, heldElsewhereLine)
		} else if errors.Is(err, api.ErrRuntimeNotRunning) {
			fmt.Fprintln(stderr, "Marshal's background service isn't running in this project. Start it with `marshal daemon` in another terminal, then try again.")
		} else {
			fmt.Fprintln(stderr, err)
		}
		return exitCode(err)
	}
	return 0
}

func (c command) policy(ctx context.Context, args []string) error {
	if len(args) != 2 || args[0] != "test" || args[1] == "" {
		return fmt.Errorf("%w: %s", model.ErrInvalid, policyTestUsage[:len(policyTestUsage)-1])
	}
	report, err := app.RunPolicyTestFile(ctx, args[1])
	if err != nil {
		return err
	}
	human := fmt.Sprintf("status=%s policy_digest=%s", report.Status, report.PolicyDigest)
	for _, result := range report.Cases {
		human += fmt.Sprintf("\n%s=%s", result.ID, result.Status)
		if result.Diff != "" {
			human += " diff=" + result.Diff
		}
		if result.Reason != "" {
			human += " reason=" + string(result.Reason)
		}
	}
	if err := c.print(report, human); err != nil {
		return err
	}
	switch report.Status {
	case policytest.StatusPass:
		return nil
	case policytest.StatusError:
		return policytest.ErrCaseInvalid
	default:
		return policytest.ErrExpectationMismatch
	}
}

func (c command) init(ctx context.Context) error {
	started, err := c.prepareProjectRepository(ctx)
	if err != nil {
		return err
	}
	if err := c.ensureGitBaseline(ctx); err != nil {
		c.reportStartedRepository(started, nil)
		return err
	}
	layout, committed, err := initializeProject(ctx, c.root)
	// A repository Marshal just started is described in one line, both
	// commits included, so the separate notice would repeat it.
	c.reportStartedRepository(started, committed)
	if err != nil {
		return err
	}
	human := "initialized " + layout.RuntimeDir
	if notice := initCommitNotice(committed); notice != "" && !started.Started {
		human += "\n" + notice
	}
	if committed == nil {
		committed = []string{}
	}
	return c.print(map[string]any{"status": "initialized", "runtime_dir": layout.RuntimeDir, "committed_files": committed}, human)
}

func (c command) doctor(ctx context.Context, args []string) int {
	probeProviders := false
	for _, arg := range args {
		if arg == "--probe-providers" || arg == "--deep" {
			probeProviders = true
		}
	}
	report := doctor.Check(ctx, c.root, doctor.Options{ProbeProviders: probeProviders})
	if c.json {
		if err := c.print(report, string(report.Verdict)); err != nil {
			fmt.Fprintln(c.stderr, err)
			return 1
		}
	} else {
		fmt.Fprintln(c.stdout, report.FormattedText())
	}
	if report.Verdict == doctor.Pass {
		return 0
	}
	return 2
}

func (c command) daemon(ctx context.Context) error {
	layout, err := app.Bootstrap(ctx, c.root)
	if err != nil {
		return err
	}
	if data, readErr := os.ReadFile(layout.PID); readErr == nil {
		var oldPID int
		if _, scanErr := fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &oldPID); scanErr == nil && oldPID > 0 {
			if killErr := syscall.Kill(oldPID, 0); killErr == syscall.ESRCH {
				_ = os.Remove(layout.PID)
				_ = os.Remove(layout.Socket)
			}
		}
	}
	pid, err := os.OpenFile(layout.PID, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("%w: create daemon PID file: %v", model.ErrConflict, err)
	}
	if _, err := fmt.Fprintln(pid, os.Getpid()); err != nil {
		pid.Close()
		os.Remove(layout.PID)
		return err
	}
	if err := pid.Close(); err != nil {
		os.Remove(layout.PID)
		return err
	}
	defer os.Remove(layout.PID)
	runtime, err := app.Open(ctx, c.root)
	if err != nil {
		return err
	}
	defer runtime.Close()
	return api.NewServer(runtime).Serve(ctx, layout.Socket)
}

func (c command) client() (*api.Client, error) {
	layout, err := project.Discover(c.root)
	if err != nil {
		return nil, err
	}
	return api.NewClient(layout.Socket), nil
}

func (c command) status(ctx context.Context) error {
	client, err := c.client()
	if err != nil {
		return err
	}
	value, _, err := client.Status(ctx)
	if notRunning(err) {
		return c.statusLocal(ctx)
	}
	if err != nil {
		return err
	}
	// The service answers the counts (and --json); the run and the
	// Marshal provider are read from the store it shares.
	human := ""
	if !c.json {
		human = statusOnlyText(value)
		if view, err := c.readLocal(ctx); err == nil {
			human = view.statusText()
		}
	}
	return c.print(value, human)
}

func (c command) agent(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "register" {
		return fmt.Errorf("%w: expected agent register", model.ErrInvalid)
	}
	set := flag.NewFlagSet("agent register", flag.ContinueOnError)
	set.SetOutput(c.stderr)
	name := set.String("name", "", "agent display name")
	role := set.String("role", "", "agent role")
	provider := set.String("provider", "", "agent model provider")
	modelName := set.String("model", "", "agent model")
	if err := set.Parse(args[1:]); err != nil {
		return fmt.Errorf("%w: %v", model.ErrInvalid, err)
	}
	client, err := c.client()
	if err != nil {
		return err
	}
	value, _, err := client.RegisterAgent(ctx, app.RegisterAgentRequest{Name: *name, Role: model.Role(*role), ModelProvider: *provider, ModelName: *modelName})
	if err != nil {
		return err
	}
	return c.print(value, value.ID)
}

func (c command) agents(ctx context.Context) error {
	client, err := c.client()
	if err != nil {
		return err
	}
	value, _, err := client.Agents(ctx)
	if err != nil {
		return err
	}
	return c.print(value, fmt.Sprintf("%d agents", len(value)))
}

func (c command) tasks(ctx context.Context) error {
	client, err := c.client()
	if err != nil {
		return err
	}
	value, _, err := client.Tasks(ctx)
	if notRunning(err) {
		return c.tasksLocal(ctx)
	}
	if err != nil {
		return err
	}
	return c.print(value, fmt.Sprintf("%d tasks", len(value)))
}

func (c command) task(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: task subcommand required", model.ErrInvalid)
	}
	switch args[0] {
	case "import":
		return c.taskImport(ctx, args[1:])
	case "show":
		if len(args) != 2 {
			return fmt.Errorf("%w: task show requires one ID", model.ErrInvalid)
		}
		client, err := c.client()
		if err != nil {
			return err
		}
		value, _, err := client.Task(ctx, args[1])
		if err != nil {
			return err
		}
		return c.print(value, fmt.Sprintf("%s %s %s", value.ID, value.Status, value.Title))
	case "claim":
		return c.taskClaim(ctx, args[1:])
	case "release":
		if len(args) != 2 {
			return fmt.Errorf("%w: task release requires one ID", model.ErrInvalid)
		}
		client, err := c.client()
		if err != nil {
			return err
		}
		requestID, err := client.Release(ctx, app.ReleaseRequest{TaskID: args[1]})
		if err != nil {
			return err
		}
		return c.print(map[string]string{"task_id": args[1], "request_id": requestID}, "released "+args[1])
	default:
		return fmt.Errorf("%w: unknown task subcommand %s", model.ErrInvalid, args[0])
	}
}

func (c command) taskImport(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: task import requires a JSON file", model.ErrInvalid)
	}
	path := args[0]
	dryRun := false
	for _, arg := range args[1:] {
		if arg == "--dry-run" {
			dryRun = true
		} else {
			return fmt.Errorf("%w: unknown import option %s", model.ErrInvalid, arg)
		}
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	tasks, err := model.DecodeTasks(file)
	if err != nil {
		return err
	}
	if dryRun {
		return c.print(tasks, taskIDs(tasks))
	}
	client, err := c.client()
	if err != nil {
		return err
	}
	value, _, err := client.ImportTasks(ctx, tasks)
	if err != nil {
		return err
	}
	return c.print(value, fmt.Sprintf("added=%d matched=%d", value.Added, value.Matched))
}

func (c command) taskClaim(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: task claim requires an ID", model.ErrInvalid)
	}
	set := flag.NewFlagSet("task claim", flag.ContinueOnError)
	set.SetOutput(c.stderr)
	agent := set.String("agent", "", "agent ID")
	revision := set.Int64("revision", 0, "expected task revision")
	if err := set.Parse(args[1:]); err != nil {
		return fmt.Errorf("%w: %v", model.ErrInvalid, err)
	}
	client, err := c.client()
	if err != nil {
		return err
	}
	agentID, err := resolveAgent(ctx, client, *agent)
	if err != nil {
		return err
	}
	value, _, err := client.Claim(ctx, app.ClaimRequest{TaskID: args[0], AgentID: agentID, ExpectedRevision: *revision})
	if err != nil {
		return err
	}
	return c.print(value, "claimed "+args[0]+" lease="+value.Lease.ID)
}

func (c command) run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: run requires a task ID", model.ErrInvalid)
	}
	set := flag.NewFlagSet("run", flag.ContinueOnError)
	set.SetOutput(c.stderr)
	adapterName := set.String("adapter", "codex", "worker adapter")
	modelName := set.String("model", "", "model name (passed only to adapters with a verified native model selector)")
	agent := set.String("agent", "", "agent ID")
	revision := set.Int64("revision", 0, "expected task revision")
	if err := set.Parse(args[1:]); err != nil {
		return fmt.Errorf("%w: %v", model.ErrInvalid, err)
	}
	client, err := c.client()
	if err != nil {
		return err
	}
	agentID, err := resolveAgent(ctx, client, *agent)
	if err != nil {
		return err
	}
	value, _, err := client.Run(ctx, app.RunRequest{TaskID: args[0], AgentID: agentID, Adapter: *adapterName, Model: *modelName, ExpectedRevision: *revision})
	if err != nil {
		return err
	}
	human := fmt.Sprintf("%s %s %s isolation=%s", value.TaskID, value.Status, value.ResultCommit, value.Isolation.Level)
	if value.Isolation.Level == model.IsolationProcessOnly {
		human += " (DEGRADED: unsandboxed process-only execution)"
	}
	return c.print(value, human)
}

func resolveAgent(ctx context.Context, client *api.Client, requested string) (string, error) {
	if requested != "" {
		return requested, nil
	}
	agents, _, err := client.Agents(ctx)
	if err != nil {
		return "", err
	}
	eligible := make([]model.Agent, 0, len(agents))
	for _, agent := range agents {
		if agent.Status != model.AgentDisabled {
			eligible = append(eligible, agent)
		}
	}
	if len(eligible) != 1 {
		return "", fmt.Errorf("%w: --agent is required unless exactly one enabled agent is registered", model.ErrInvalid)
	}
	return eligible[0].ID, nil
}

func (c command) events(ctx context.Context) error {
	client, err := c.client()
	if err != nil {
		return err
	}
	value, _, err := client.Events(ctx)
	if err != nil {
		return err
	}
	return c.print(value, fmt.Sprintf("%d events", len(value)))
}

func (c command) artifacts(ctx context.Context) error {
	client, err := c.client()
	if err != nil {
		return err
	}
	value, _, err := client.Artifacts(ctx)
	if err != nil {
		return err
	}
	return c.print(value, fmt.Sprintf("%d artifacts", len(value)))
}

func (c command) verify(ctx context.Context, args []string) error {
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	if len(args) == 0 {
		var err error
		args, err = app.DefaultVerificationCommand(c.root)
		if err != nil {
			return err
		}
	}
	client, err := c.client()
	if err != nil {
		return err
	}
	value, _, err := client.Verify(ctx, app.VerifyRequest{Command: args})
	if err != nil {
		return err
	}
	return c.print(value, "PASS "+value.OutputDigest)
}

func (c command) reconcile(ctx context.Context, args []string) error {
	set := flag.NewFlagSet("reconcile", flag.ContinueOnError)
	set.SetOutput(c.stderr)
	fileState := set.String("file-state", "", "JSON checkpoint to compare")
	if err := set.Parse(args); err != nil {
		return fmt.Errorf("%w: %v", model.ErrInvalid, err)
	}
	client, err := c.client()
	if err != nil {
		return err
	}
	value, _, err := client.Reconcile(ctx, app.ReconcileRequest{FileState: *fileState})
	if err != nil {
		return err
	}
	return c.print(value, fmt.Sprintf("%s conflicts=%d", value.Status, len(value.Conflicts)))
}
func (c command) logs(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: logs requires a TASK-ID", model.ErrInvalid)
	}
	taskID := args[0]
	client, err := c.client()
	if err != nil {
		return err
	}
	events, _, err := client.Events(ctx)
	if err != nil {
		return err
	}
	var taskEvents []model.Event
	for _, e := range events {
		if e.TaskID == taskID {
			taskEvents = append(taskEvents, e)
		}
	}
	artifacts, _, _ := client.Artifacts(ctx)
	var taskArtifacts []model.Artifact
	for _, a := range artifacts {
		for _, tid := range a.TaskIDs {
			if tid == taskID {
				taskArtifacts = append(taskArtifacts, a)
				break
			}
		}
	}
	result := map[string]any{
		"task_id":   taskID,
		"events":    taskEvents,
		"artifacts": taskArtifacts,
	}
	if c.json {
		return c.print(result, "")
	}
	var lines []string
	lines = append(lines, fmt.Sprintf("=== Logs & Events for %s ===", taskID))
	lines = append(lines, fmt.Sprintf("Events (%d):", len(taskEvents)))
	for _, e := range taskEvents {
		lines = append(lines, fmt.Sprintf("  [%s] %s", e.Timestamp.Format(time.RFC3339), e.Type))
	}
	lines = append(lines, fmt.Sprintf("Artifacts (%d):", len(taskArtifacts)))
	for _, a := range taskArtifacts {
		lines = append(lines, fmt.Sprintf("  [%s] %s size=%d commit=%s", a.ID, a.Kind, a.Size, a.SourceCommit))
	}
	return c.print(result, strings.Join(lines, "\n"))
}

func (c command) cancel(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: cancel requires a TASK-ID", model.ErrInvalid)
	}
	taskID := args[0]
	client, err := c.client()
	if err != nil {
		return err
	}
	requestID, err := client.CancelTask(ctx, taskID)
	if err != nil {
		return err
	}
	return c.print(map[string]string{"task_id": taskID, "request_id": requestID}, fmt.Sprintf("cancelled task %s", taskID))
}

func (c command) adapters(ctx context.Context) error {
	names := []string{"codex", "agy", "claude", "opencode"}
	list := make([]map[string]any, 0, len(names))
	for _, name := range names {
		binary, err := project.FindBinary(name)
		entry := map[string]any{"name": name, "available": false, "binary": binary, "version": "", "detail": "CLI missing"}
		if err == nil {
			obs := observeAdapterCLI(ctx, name, binary)
			entry["available"], entry["version"], entry["detail"] = obs.Available, obs.Version, obs.Detail
			if obs.Pinned != "" {
				entry["pinned_version"] = obs.Pinned
			}
		}
		list = append(list, entry)
	}
	if !c.json {
		var lines []string
		lines = append(lines, "=== MARSHAL Provider Adapters ===")
		for _, a := range list {
			avail := "UNAVAILABLE"
			if a["available"].(bool) {
				avail = "AVAILABLE"
			}
			line := fmt.Sprintf("  %-10s %-12s binary=%-40s version=%s", a["name"], avail, a["binary"], a["version"])
			if d, _ := a["detail"].(string); d != "" {
				line += " (" + d + ")"
			}
			lines = append(lines, line)
		}
		return c.print(list, strings.Join(lines, "\n"))
	}
	return c.print(list, fmt.Sprintf("%d adapters", len(list)))
}

func (c command) adapter(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "probe" {
		return fmt.Errorf("%w: expected adapter probe <name>", model.ErrInvalid)
	}
	if len(args) < 2 {
		return fmt.Errorf("%w: adapter name required", model.ErrInvalid)
	}
	name := args[1]
	if name == "antigravity" {
		name = "agy"
	}
	if name != "codex" && name != "claude" && name != "opencode" && name != "agy" {
		return fmt.Errorf("%w: unsupported adapter %s", model.ErrInvalid, name)
	}
	binary, err := project.FindBinary(name)
	if err != nil {
		return c.print(map[string]any{
			"name": name, "available": false, "error": err.Error(),
		}, fmt.Sprintf("adapter %s: unavailable (CLI missing)", name))
	}
	obs := observeAdapterCLI(ctx, name, binary)
	state := "available"
	if !obs.Available {
		state = "unavailable"
	}
	out := map[string]any{"name": name, "available": obs.Available, "binary": binary, "version": obs.Version, "detail": obs.Detail}
	if obs.Pinned != "" {
		out["pinned_version"] = obs.Pinned
	}
	return c.print(out, fmt.Sprintf("adapter %s: %s, version %s (%s)", name, state, obs.Version, obs.Detail))
}

func (c command) mcp(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: mcp subcommand required (serve|status)", model.ErrInvalid)
	}
	switch args[0] {
	case "relay":
		return c.handleMCPRelay(ctx, args[1:])
	case "serve":
		if handled, err := c.handleMCPServeStdio(ctx, args[1:]); handled {
			return err
		}
		flags := flag.NewFlagSet("mcp serve", flag.ContinueOnError)
		flags.SetOutput(c.stderr)
		listen := flags.String("listen", "127.0.0.1:8080", "listen address (host:port)")
		insecure := flags.Bool("insecure", false, "allow unauthenticated execution (loopback only)")
		if err := flags.Parse(args[1:]); err != nil {
			return fmt.Errorf("%w: %v", model.ErrInvalid, err)
		}
		if *insecure && !isLoopbackAddr(*listen) {
			return fmt.Errorf("%w: --insecure mode is forbidden on non-loopback address %s", model.ErrInvalid, *listen)
		}

		runtime, err := app.Open(ctx, c.root)
		if err != nil {
			return err
		}
		defer runtime.Close()

		var srv *mcp.Server
		if *insecure {
			fmt.Fprintln(c.stderr, "WARNING: Running MCP server in insecure mode on loopback (authentication disabled)")
			srv = mcp.NewServer(runtime)
		} else {
			layout, err := app.Bootstrap(ctx, c.root)
			if err != nil {
				return err
			}
			authMgr := auth.NewManager(layout.RuntimeDir)
			srv = mcp.NewServerWithAuth(runtime, authMgr)
		}

		fmt.Fprintf(c.stdout, "Starting MARSHAL MCP server on http://%s\n", *listen)
		server := httpsrv.NewServer(httpsrv.Config{
			Addr:    *listen,
			Handler: srv.Handler(),
		})
		errCh := make(chan error, 1)
		go func() {
			errCh <- server.ListenAndServe()
		}()
		select {
		case <-ctx.Done():
			shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = server.Shutdown(shutCtx)
			return ctx.Err()
		case err := <-errCh:
			return err
		}
	case "stdio":
		flags := flag.NewFlagSet("mcp stdio", flag.ContinueOnError)
		flags.SetOutput(c.stderr)
		root := flags.String("project", c.root, "project root")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return model.ErrInvalid
		}
		runtime, err := app.Open(ctx, *root)
		if err != nil {
			return err
		}
		defer runtime.Close()
		return mcp.NewSessionServer(runtime, auth.NewManager(filepath.Join(runtime.ProjectRoot(), projectid.StateDirName))).ServeStdio(ctx, c.stdin, c.stdout)

	case "status":
		flags := flag.NewFlagSet("mcp status", flag.ContinueOnError)
		flags.SetOutput(c.stderr)
		endpoint := flags.String("endpoint", "127.0.0.1:8080", "MCP server endpoint address to probe (host:port or URL)")
		flags.StringVar(endpoint, "listen", "127.0.0.1:8080", "alias for --endpoint")
		flags.StringVar(endpoint, "addr", "127.0.0.1:8080", "alias for --endpoint")
		token := flags.String("token", "", "bearer token for authenticated probe")
		timeout := flags.Duration("timeout", 1*time.Second, "probe timeout")
		if err := flags.Parse(args[1:]); err != nil {
			return fmt.Errorf("%w: %v", model.ErrInvalid, err)
		}
		if len(flags.Args()) > 0 {
			*endpoint = flags.Arg(0)
		}

		addr := *endpoint
		if strings.HasPrefix(addr, "http://") {
			addr = strings.TrimPrefix(addr, "http://")
		} else if strings.HasPrefix(addr, "https://") {
			addr = strings.TrimPrefix(addr, "https://")
		}
		if idx := strings.Index(addr, "/"); idx != -1 {
			addr = addr[:idx]
		}
		if !isLoopbackAddr(addr) {
			return fmt.Errorf("%w: mcp status probe is restricted to local loopback addresses (got %s)", model.ErrInvalid, *endpoint)
		}

		probeURL := *endpoint
		if !strings.HasPrefix(probeURL, "http://") && !strings.HasPrefix(probeURL, "https://") {
			probeURL = "http://" + probeURL
		}
		if !strings.HasSuffix(probeURL, "/") && !strings.HasSuffix(probeURL, "/mcp") {
			probeURL = probeURL + "/"
		}

		probeCtx, cancel := context.WithTimeout(ctx, *timeout)
		defer cancel()

		reqBody := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"` + mcp.ProtocolVersion2026 + `"}}`
		req, err := http.NewRequestWithContext(probeCtx, http.MethodPost, probeURL, strings.NewReader(reqBody))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("MCP-Protocol-Version", mcp.ProtocolVersion2026)
		if *token != "" {
			req.Header.Set("Authorization", "Bearer "+*token)
		}

		client := &http.Client{
			Transport: &http.Transport{
				Proxy: nil, // direct loopback probe only, bypass any host proxy
			},
			Timeout: *timeout,
		}

		var authMgr *auth.Manager
		if layout, layoutErr := project.Discover(c.root); layoutErr == nil && layout.RuntimeDir != "" {
			authMgr = auth.NewManager(layout.RuntimeDir)
		}
		providerStates := mcp.CheckProviderStates(authMgr)
		formatWithProviders := func(header string) string {
			var sb strings.Builder
			sb.WriteString(header)
			sb.WriteString("\nProvider connections:\n")
			for _, p := range []string{"claude", "codex", "opencode", "antigravity"} {
				ps := providerStates[p]
				sb.WriteString(fmt.Sprintf("  %-12s %s - %s\n", p+":", strings.ToUpper(ps.Status), ps.Detail))
			}
			return strings.TrimRight(sb.String(), "\n")
		}

		resp, err := client.Do(req)
		if err != nil {
			return c.print(map[string]any{
				"status":           "offline",
				"endpoint":         probeURL,
				"protocol_version": mcp.ProtocolVersion2026,
				"healthy":          false,
				"reason":           err.Error(),
				"providers":        providerStates,
			}, formatWithProviders(fmt.Sprintf("MCP server status OFFLINE at %s (%v)", probeURL, err)))
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			var rpcResp struct {
				Result struct {
					ProtocolVersion string            `json:"protocolVersion"`
					ServerInfo      map[string]string `json:"serverInfo"`
				} `json:"result"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&rpcResp)
			proto := rpcResp.Result.ProtocolVersion
			if proto == "" {
				proto = mcp.ProtocolVersion2026
			}
			srvName := rpcResp.Result.ServerInfo["name"]
			srvVer := rpcResp.Result.ServerInfo["version"]
			return c.print(map[string]any{
				"status":           "online",
				"endpoint":         probeURL,
				"protocol_version": proto,
				"healthy":          true,
				"server_info":      rpcResp.Result.ServerInfo,
				"providers":        providerStates,
			}, formatWithProviders(fmt.Sprintf("MCP server status ONLINE at %s (protocol %s, server %s %s)", probeURL, proto, srvName, srvVer)))
		}

		if resp.StatusCode == http.StatusUnauthorized {
			return c.print(map[string]any{
				"status":           "online",
				"endpoint":         probeURL,
				"protocol_version": mcp.ProtocolVersion2026,
				"healthy":          true,
				"authenticated":    true,
				"reason":           "authentication required (bearer token)",
				"providers":        providerStates,
			}, formatWithProviders(fmt.Sprintf("MCP server status ONLINE at %s (authentication required, protocol %s)", probeURL, mcp.ProtocolVersion2026)))
		}

		return c.print(map[string]any{
			"status":           "unhealthy",
			"endpoint":         probeURL,
			"http_code":        resp.StatusCode,
			"protocol_version": mcp.ProtocolVersion2026,
			"healthy":          false,
			"reason":           fmt.Sprintf("HTTP %d", resp.StatusCode),
			"providers":        providerStates,
		}, formatWithProviders(fmt.Sprintf("MCP server status UNHEALTHY at %s (HTTP %d)", probeURL, resp.StatusCode)))
	default:
		return fmt.Errorf("%w: unknown mcp subcommand %s", model.ErrInvalid, args[0])
	}
}

func (c command) a2a(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: a2a subcommand required (serve|status)", model.ErrInvalid)
	}
	switch args[0] {
	case "serve":
		flags := flag.NewFlagSet("a2a serve", flag.ContinueOnError)
		flags.SetOutput(c.stderr)
		listen := flags.String("listen", "127.0.0.1:8081", "listen address (host:port)")
		insecure := flags.Bool("insecure", false, "allow unauthenticated execution (loopback only)")
		if err := flags.Parse(args[1:]); err != nil {
			return fmt.Errorf("%w: %v", model.ErrInvalid, err)
		}
		if *insecure && !isLoopbackAddr(*listen) {
			return fmt.Errorf("%w: --insecure mode is forbidden on non-loopback address %s", model.ErrInvalid, *listen)
		}

		runtime, err := app.Open(ctx, c.root)
		if err != nil {
			return err
		}
		defer runtime.Close()

		var srv *a2a.Server
		if *insecure {
			fmt.Fprintln(c.stderr, "WARNING: Running A2A server in insecure mode on loopback (authentication disabled)")
			srv, err = a2a.NewInsecureServer(runtime, *listen)
		} else {
			layout, err := app.Bootstrap(ctx, c.root)
			if err != nil {
				return err
			}
			authMgr := auth.NewManager(layout.RuntimeDir)
			srv, err = a2a.NewServerWithAuth(runtime, authMgr)
		}

		if err != nil {
			return err
		}
		fmt.Fprintf(c.stdout, "Starting MARSHAL A2A server on http://%s\n", *listen)
		server := httpsrv.NewServer(httpsrv.Config{
			Addr:    *listen,
			Handler: srv.Handler(),
		})
		errCh := make(chan error, 1)
		go func() {
			errCh <- server.ListenAndServe()
		}()
		select {
		case <-ctx.Done():
			shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = server.Shutdown(shutCtx)
			return ctx.Err()
		case err := <-errCh:
			return err
		}
	case "status":
		return c.print(map[string]any{
			"status": "unknown", "protocol_version": a2a.ProtocolVersion100, "reason": "no live endpoint probe performed",
		}, "A2A server status UNKNOWN (no live endpoint probe performed)")
	default:
		return fmt.Errorf("%w: unknown a2a subcommand %s", model.ErrInvalid, args[0])
	}
}

func (c command) print(value any, human string) error {
	if !c.json {
		_, err := fmt.Fprintln(c.stdout, human)
		return err
	}
	encoder := json.NewEncoder(c.stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func taskIDs(tasks []model.Task) string {
	ids := make([]string, len(tasks))
	for i := range tasks {
		ids[i] = tasks[i].ID
	}
	return strings.Join(ids, "\n")
}

func (c command) auth(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: missing auth subcommand (token)", model.ErrInvalid)
	}
	if args[0] != "token" || len(args) < 2 {
		return fmt.Errorf("%w: usage: marshal auth token <create|list|revoke>", model.ErrInvalid)
	}
	layout, err := app.Bootstrap(ctx, c.root)
	if err != nil {
		return err
	}
	mgr := auth.NewManager(layout.RuntimeDir)

	sub := args[1]
	switch sub {
	case "create":
		flags := flag.NewFlagSet("token create", flag.ContinueOnError)
		flags.SetOutput(c.stderr)
		name := flags.String("name", "", "token name")
		kind := flags.String("kind", "mcp_client", "principal kind (mcp_client|a2a_agent|local_user)")
		caps := flags.String("capabilities", "", "comma-separated capabilities or 'all'")
		if err := flags.Parse(args[2:]); err != nil {
			return fmt.Errorf("%w: %v", model.ErrInvalid, err)
		}
		if *name == "" {
			return fmt.Errorf("%w: --name is required", model.ErrInvalid)
		}
		var requestedCaps []string
		if *caps != "" {
			requestedCaps = strings.Split(*caps, ",")
		} else {
			requestedCaps = auth.DefaultCapabilitiesForKind(auth.PrincipalKind(*kind))
		}
		validatedCaps, err := auth.ValidateCapabilities(requestedCaps)
		if err != nil {
			return fmt.Errorf("%w: %v", model.ErrInvalid, err)
		}
		plaintext, record, err := mgr.CreateToken(*name, auth.PrincipalKind(*kind), validatedCaps)
		if err != nil {
			return err
		}
		return c.print(map[string]any{
			"id":           record.ID,
			"name":         record.Name,
			"kind":         record.Kind,
			"capabilities": record.Capabilities,
			"token":        plaintext,
			"created":      record.CreatedAt,
		}, fmt.Sprintf("Created Token ID: %s\nPrincipal: %s (%s)\nCapabilities: %s\nPlaintext Token: %s\n(Keep this token secret; it will not be shown again)", record.ID, record.Name, record.Kind, strings.Join(record.Capabilities, ", "), plaintext))

	case "list":
		tokens, err := mgr.ListTokens()
		if err != nil {
			return err
		}
		tokens = auth.RedactTokenMetadata(tokens)
		var lines []string
		for _, t := range tokens {
			lines = append(lines, fmt.Sprintf("%s\t%s\t%s\trevoked=%t", t.ID, t.Name, t.Kind, t.Revoked))
		}
		return c.print(tokens, strings.Join(lines, "\n"))

	case "revoke":
		flags := flag.NewFlagSet("token revoke", flag.ContinueOnError)
		flags.SetOutput(c.stderr)
		id := flags.String("id", "", "token ID")
		if err := flags.Parse(args[2:]); err != nil {
			return fmt.Errorf("%w: %v", model.ErrInvalid, err)
		}
		if *id == "" {
			return fmt.Errorf("%w: --id is required", model.ErrInvalid)
		}
		if err := mgr.RevokeToken(*id); err != nil {
			return err
		}
		return c.print(map[string]any{"id": *id, "revoked": true}, fmt.Sprintf("Revoked Token ID: %s", *id))

	default:
		return fmt.Errorf("%w: unknown token subcommand %s", model.ErrInvalid, sub)
	}
}

func (c command) legal(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: legal subcommand required (audit, export)", model.ErrInvalid)
	}

	asJSON := c.json
	sub := args[0]
	subArgs := args[1:]

	if sub == "--json" && len(subArgs) > 0 {
		asJSON = true
		sub = subArgs[0]
		subArgs = subArgs[1:]
	}

	switch sub {
	case "audit":
		for _, a := range subArgs {
			if a == "--json" {
				asJSON = true
			}
		}
		report, err := legal.RunAudit(ctx, c.root)
		if err != nil {
			return err
		}
		if asJSON {
			data, err := report.ToJSON()
			if err != nil {
				return err
			}
			fmt.Fprintln(c.stdout, string(data))
			return nil
		}
		fmt.Fprint(c.stdout, report.ToTerminal())
		return nil

	case "export":
		flags := flag.NewFlagSet("legal export", flag.ContinueOnError)
		flags.SetOutput(c.stderr)
		output := flags.String("output", "", "output tar.gz file path")
		if err := flags.Parse(subArgs); err != nil {
			return fmt.Errorf("%w: %v", model.ErrInvalid, err)
		}
		if *output == "" {
			return fmt.Errorf("%w: --output is required", model.ErrInvalid)
		}

		res, err := legal.ExportPack(ctx, c.root, *output)
		if err != nil {
			return err
		}

		if asJSON {
			data, err := json.MarshalIndent(res, "", "  ")
			if err != nil {
				return err
			}
			fmt.Fprintln(c.stdout, string(data))
			return nil
		}

		fmt.Fprintf(c.stdout, "Evidence pack: %s\n", res.OutputPath)
		fmt.Fprintf(c.stdout, "Source HEAD:   %s\n", res.SourceHEAD)
		fmt.Fprintf(c.stdout, "SHA-256:       %s\n", res.ArchiveSHA256)
		fmt.Fprintf(c.stdout, "Status:        %s\n", res.OverallStatus)
		return nil

	default:
		return fmt.Errorf("%w: unknown legal subcommand %s", model.ErrInvalid, sub)
	}
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	if errors.Is(err, model.ErrInvalid) {
		return 2
	}
	if errors.Is(err, model.ErrPolicyDenied) {
		return 3
	}
	if errors.Is(err, model.ErrApprovalRequired) {
		return 4
	}
	if errors.Is(err, model.ErrConflict) {
		return 5
	}
	if errors.Is(err, model.ErrUnavailable) {
		return 6
	}
	return 1
}

func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	host = strings.TrimSpace(strings.ToLower(host))
	if host == "" || host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (c command) gc(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: gc target required (worktrees|artifacts)", model.ErrInvalid)
	}
	target := args[0]
	switch target {
	case "worktrees":
		flags := flag.NewFlagSet("gc worktrees", flag.ContinueOnError)
		flags.SetOutput(c.stderr)
		dryRun := flags.Bool("dry-run", false, "simulate garbage collection without deleting files")
		ttlStr := flags.String("ttl", "24h", "maximum retention duration for stale worktrees")
		if err := flags.Parse(args[1:]); err != nil {
			return fmt.Errorf("%w: %v", model.ErrInvalid, err)
		}

		ttl, err := time.ParseDuration(*ttlStr)
		if err != nil {
			return fmt.Errorf("%w: invalid --ttl format: %v", model.ErrInvalid, err)
		}

		runtime, err := app.Open(ctx, c.root)
		if err != nil {
			return err
		}
		defer runtime.Close()

		res, err := runtime.GCWorktrees(ctx, *dryRun, ttl)
		if err != nil {
			return err
		}

		return c.print(res, fmt.Sprintf("Inspected: %d, Cleaned: %d, Retained: %d, Skipped Dirty: %d",
			res.InspectedCount, len(res.CleanedPaths), len(res.RetainedPaths), len(res.SkippedDirty)))

	case "artifacts":
		flags := flag.NewFlagSet("gc artifacts", flag.ContinueOnError)
		flags.SetOutput(c.stderr)
		dryRun := flags.Bool("dry-run", false, "simulate garbage collection without deleting files")
		ttlStr := flags.String("ttl", "24h", "maximum retention duration for unreferenced artifacts")
		maxBudget := flags.Int64("max-budget", 0, "maximum disk budget in bytes (0 for unlimited)")
		if err := flags.Parse(args[1:]); err != nil {
			return fmt.Errorf("%w: %v", model.ErrInvalid, err)
		}

		ttl, err := time.ParseDuration(*ttlStr)
		if err != nil {
			return fmt.Errorf("%w: invalid --ttl format: %v", model.ErrInvalid, err)
		}

		runtime, err := app.Open(ctx, c.root)
		if err != nil {
			return err
		}
		defer runtime.Close()

		res, err := runtime.GCArtifacts(ctx, *dryRun, ttl, *maxBudget)
		if err != nil {
			return err
		}

		return c.print(res, fmt.Sprintf("Total: %d files (%d bytes), Cleaned: %d (%d bytes), Retained: %d (%d bytes), Orphans: %d",
			res.TotalFiles, res.TotalBytes, len(res.CleanedFiles), res.CleanedBytes, len(res.RetainedFiles), res.RetainedBytes, len(res.OrphanFiles)))

	default:
		return fmt.Errorf("%w: unknown gc target %s (supported: worktrees, artifacts)", model.ErrInvalid, target)
	}
}

func (c command) state(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: state subcommand required (backup|verify-backup|restore)", model.ErrInvalid)
	}

	sub := args[0]
	switch sub {
	case "backup":
		flags := flag.NewFlagSet("state backup", flag.ContinueOnError)
		flags.SetOutput(c.stderr)
		output := flags.String("output", "", "output backup file path")
		if err := flags.Parse(args[1:]); err != nil {
			return fmt.Errorf("%w: %v", model.ErrInvalid, err)
		}

		runtime, err := app.Open(ctx, c.root)
		if err != nil {
			return err
		}
		defer runtime.Close()

		meta, err := runtime.BackupState(ctx, *output)
		if err != nil {
			return err
		}

		return c.print(meta, fmt.Sprintf("Backup created: %s (Project: %s, Schema: v%d, SHA-256: %s)",
			meta.DatabaseSHA256, meta.ProjectID, meta.SchemaVersion, meta.DatabaseSHA256))

	case "verify-backup":
		if len(args) < 2 {
			return fmt.Errorf("%w: backup file path required: marshal state verify-backup BACKUP-PATH", model.ErrInvalid)
		}
		backupPath := args[1]
		meta, err := app.VerifyStateBackup(ctx, backupPath, "", 0)
		if err != nil {
			return err
		}

		return c.print(meta, fmt.Sprintf("Backup verified: valid SQLite database (Project: %s, Schema: v%d, SHA-256: %s)",
			meta.ProjectID, meta.SchemaVersion, meta.DatabaseSHA256))

	case "restore":
		if len(args) < 2 {
			return fmt.Errorf("%w: backup file path required: marshal state restore BACKUP-PATH", model.ErrInvalid)
		}
		backupPath := args[1]
		if err := app.RestoreState(ctx, c.root, backupPath); err != nil {
			return err
		}

		return c.print(map[string]any{"status": "restored", "backup": backupPath}, fmt.Sprintf("State database successfully restored from %s", backupPath))

	default:
		return fmt.Errorf("%w: unknown state subcommand %s (supported: backup, verify-backup, restore)", model.ErrInvalid, sub)
	}
}
