package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Zen1th53/marshal/internal/a2a"
	"github.com/Zen1th53/marshal/internal/api"
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/auth"
	"github.com/Zen1th53/marshal/internal/doctor"
	"github.com/Zen1th53/marshal/internal/legal"
	"github.com/Zen1th53/marshal/internal/mcp"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/policytest"
	"github.com/Zen1th53/marshal/internal/project"
)

var (
	Version   = "v1.0.0"
	Commit    = "head"
	BuildDate = "unknown"
)

const usage = `Usage: marshal [--json] <command> [arguments]

Commands:
  init
  doctor [--probe-providers]
  status
  agent register --name NAME --role ROLE
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
  policy test SUITE-FILE
  legal audit [--json] | legal export --output PATH
  daemon
  version
`

const policyTestUsage = "Usage: marshal policy test SUITE-FILE\n"

type command struct {
	root   string
	json   bool
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
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
	c := command{root: root, stdin: stdin, stdout: stdout, stderr: stderr}
	if len(args) > 0 && args[0] == "--json" {
		c.json = true
		args = args[1:]
	}
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprint(stdout, usage)
		return 0
	}
	if len(args) > 1 && (args[1] == "--help" || args[1] == "-h") {
		fmt.Fprint(stdout, usage)
		return 0
	}
	var err error
	switch args[0] {
	case "version", "--version", "-version", "-v":
		err = c.print(map[string]any{
			"version":        Version,
			"commit":         Commit,
			"build_date":     BuildDate,
			"schema_version": 67,
		}, fmt.Sprintf("MARSHAL %s (commit: %s, build date: %s, schema: v67)", Version, Commit, BuildDate))
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
	default:
		err = fmt.Errorf("%w: unknown command %s", model.ErrInvalid, args[0])
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitCode(err)
	}
	return 0
}

func (c command) policy(ctx context.Context, args []string) error {
	if len(args) == 2 && args[0] == "test" && (args[1] == "--help" || args[1] == "-h") {
		fmt.Fprint(c.stdout, policyTestUsage)
		return nil
	}
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
	layout, err := app.Bootstrap(ctx, c.root)
	if err != nil {
		return err
	}
	return c.print(map[string]any{"status": "initialized", "runtime_dir": layout.RuntimeDir}, "initialized "+layout.RuntimeDir)
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
	layout, err := filepath.Abs(filepath.Join(c.root, ".marshal", "runtime.sock"))
	if err != nil {
		return nil, err
	}
	return api.NewClient(layout), nil
}

func (c command) status(ctx context.Context) error {
	client, err := c.client()
	if err != nil {
		return err
	}
	value, _, err := client.Status(ctx)
	if err != nil {
		return err
	}
	return c.print(value, fmt.Sprintf("schema=%d tasks=%d agents=%d", value.SchemaVersion, value.TaskCount, value.AgentCount))
}

func (c command) agent(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "register" {
		return fmt.Errorf("%w: expected agent register", model.ErrInvalid)
	}
	set := flag.NewFlagSet("agent register", flag.ContinueOnError)
	set.SetOutput(c.stderr)
	name := set.String("name", "", "agent display name")
	role := set.String("role", "", "agent role")
	if err := set.Parse(args[1:]); err != nil {
		return fmt.Errorf("%w: %v", model.ErrInvalid, err)
	}
	client, err := c.client()
	if err != nil {
		return err
	}
	value, _, err := client.RegisterAgent(ctx, app.RegisterAgentRequest{Name: *name, Role: model.Role(*role)})
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
	value, _, err := client.Run(ctx, app.RunRequest{TaskID: args[0], AgentID: agentID, Adapter: *adapterName, ExpectedRevision: *revision})
	if err != nil {
		return err
	}
	return c.print(value, fmt.Sprintf("%s %s %s", value.TaskID, value.Status, value.ResultCommit))
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
	names := []string{"codex", "gemini", "claude", "opencode"}
	list := make([]map[string]any, 0, len(names))
	for _, name := range names {
		binary, err := project.FindBinary(name)
		available := err == nil
		version := "unknown"
		if available {
			if out, err := exec.CommandContext(ctx, binary, "--version").Output(); err == nil {
				version = strings.TrimSpace(string(out))
			}
		}
		list = append(list, map[string]any{
			"name":      name,
			"available": available,
			"binary":    binary,
			"version":   version,
		})
	}
	if !c.json {
		var lines []string
		lines = append(lines, "=== MARSHAL Provider Adapters ===")
		for _, a := range list {
			avail := "UNAVAILABLE"
			if a["available"].(bool) {
				avail = "AVAILABLE"
			}
			lines = append(lines, fmt.Sprintf("  %-10s %-12s binary=%-40s version=%s", a["name"], avail, a["binary"], a["version"]))
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
	binary, err := project.FindBinary(name)
	if err != nil {
		return c.print(map[string]any{
			"name": name, "available": false, "error": err.Error(),
		}, fmt.Sprintf("adapter %s: unavailable (CLI missing)", name))
	}
	out, err := exec.CommandContext(ctx, binary, "--version").Output()
	version := "unknown"
	if err == nil {
		version = strings.TrimSpace(string(out))
	}
	return c.print(map[string]any{
		"name": name, "available": true, "binary": binary, "version": version,
	}, fmt.Sprintf("adapter %s: available (%s)", name, version))
}

func (c command) mcp(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: mcp subcommand required (serve|status)", model.ErrInvalid)
	}
	switch args[0] {
	case "serve":
		listen := "127.0.0.1:8080"
		if len(args) >= 3 && args[1] == "--listen" {
			listen = args[2]
		}
		runtime, err := app.Open(ctx, c.root)
		if err != nil {
			return err
		}
		defer runtime.Close()
		srv := mcp.NewServer(runtime)
		fmt.Fprintf(c.stdout, "Starting MARSHAL MCP server on http://%s\n", listen)
		server := &http.Server{Addr: listen, Handler: srv.Handler()}
		return server.ListenAndServe()
	case "status":
		return c.print(map[string]any{
			"status": "ready", "protocol_version": mcp.ProtocolVersion2026,
		}, "MCP server ready (2026-07-28)")
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
		listen := "127.0.0.1:8081"
		if len(args) >= 3 && args[1] == "--listen" {
			listen = args[2]
		}
		runtime, err := app.Open(ctx, c.root)
		if err != nil {
			return err
		}
		defer runtime.Close()
		srv := a2a.NewServer(runtime)
		fmt.Fprintf(c.stdout, "Starting MARSHAL A2A server on http://%s\n", listen)
		server := &http.Server{Addr: listen, Handler: srv.Handler()}
		return server.ListenAndServe()
	case "status":
		return c.print(map[string]any{
			"status": "ready", "protocol_version": a2a.ProtocolVersion100,
		}, "A2A server ready (1.0.0)")
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
		if err := flags.Parse(args[2:]); err != nil {
			return fmt.Errorf("%w: %v", model.ErrInvalid, err)
		}
		if *name == "" {
			return fmt.Errorf("%w: --name is required", model.ErrInvalid)
		}
		plaintext, record, err := mgr.CreateToken(*name, auth.PrincipalKind(*kind), []string{"all"})
		if err != nil {
			return err
		}
		return c.print(map[string]any{
			"id":      record.ID,
			"name":    record.Name,
			"kind":    record.Kind,
			"token":   plaintext,
			"created": record.CreatedAt,
		}, fmt.Sprintf("Created Token ID: %s\nPlaintext Token: %s\n(Keep this token secret; it will not be shown again)", record.ID, plaintext))

	case "list":
		tokens, err := mgr.ListTokens()
		if err != nil {
			return err
		}
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
