package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Zen1th53/slaves/internal/api"
	"github.com/Zen1th53/slaves/internal/app"
	"github.com/Zen1th53/slaves/internal/doctor"
	"github.com/Zen1th53/slaves/internal/model"
)

const usage = `Usage: slaves [--json] <command> [arguments]

Commands:
  init
  doctor
  status
  agent register --name NAME --role ROLE
  agents
  tasks
  task import tasks.json [--dry-run]
  task show TASK-ID
  task claim TASK-ID --agent AGENT-ID [--revision N]
  task release TASK-ID
  run TASK-ID --adapter codex --agent AGENT-ID
  events
  artifacts
  verify [-- command args...]
  reconcile --file-state state.json
  daemon
`

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
	case "init":
		err = c.init(ctx)
	case "doctor":
		return c.doctor(ctx)
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
	case "events":
		err = c.events(ctx)
	case "artifacts":
		err = c.artifacts(ctx)
	case "verify":
		err = c.verify(ctx, args[1:])
	case "reconcile":
		err = c.reconcile(ctx, args[1:])
	default:
		err = fmt.Errorf("%w: unknown command %s", model.ErrInvalid, args[0])
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitCode(err)
	}
	return 0
}

func (c command) init(ctx context.Context) error {
	layout, err := app.Bootstrap(ctx, c.root)
	if err != nil {
		return err
	}
	return c.print(map[string]any{"status": "initialized", "runtime_dir": layout.RuntimeDir}, "initialized "+layout.RuntimeDir)
}

func (c command) doctor(ctx context.Context) int {
	report := doctor.Check(ctx, c.root, doctor.Options{})
	if err := c.print(report, string(report.Verdict)); err != nil {
		fmt.Fprintln(c.stderr, err)
		return 1
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
	layout, err := filepath.Abs(filepath.Join(c.root, ".slaves", "runtime.sock"))
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
	value, _, err := client.Claim(ctx, app.ClaimRequest{TaskID: args[0], AgentID: *agent, ExpectedRevision: *revision})
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
	value, _, err := client.Run(ctx, app.RunRequest{TaskID: args[0], AgentID: *agent, Adapter: *adapterName, ExpectedRevision: *revision})
	if err != nil {
		return err
	}
	return c.print(value, fmt.Sprintf("%s %s %s", value.TaskID, value.Status, value.ResultCommit))
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
	if err := set.Parse(args); err != nil { return fmt.Errorf("%w: %v", model.ErrInvalid, err) }
	client, err := c.client(); if err != nil { return err }
	value, _, err := client.Reconcile(ctx, app.ReconcileRequest{FileState: *fileState}); if err != nil { return err }
	return c.print(value, fmt.Sprintf("%s conflicts=%d", value.Status, len(value.Conflicts)))
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
