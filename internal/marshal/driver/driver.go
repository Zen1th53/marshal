// Package driver launches a Marshal task on a worker agent without a
// terminal and assembles the hand-in from what the runtime itself observes.
//
// The split between what the runtime observed and what the worker reported
// is the point of the package. A worker's account of its own work is a claim;
// the diff, the files touched and the re-run checks come from git and from
// commands MARSHAL ran, so a hand-in stays trustworthy even when the worker's
// stream is wrong or hostile.
package driver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/hostgit"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/worker"
)

// maxOutput bounds each captured output. A worker or a check can print
// without limit; the hand-in keeps enough to read and states the truncation.
const maxOutput = 64 << 10

// maxStream bounds how much of a worker's JSON stream is kept for parsing
// its reported actions.
const maxStream = 8 << 20

// DefaultCheckTimeout bounds one acceptance check when a driver sets none.
const DefaultCheckTimeout = 10 * time.Minute

// maxGitOutput bounds one git command's output, such as the hand-in diff.
const maxGitOutput = 16 << 20

// ErrInvalidRequest reports a request that cannot be dispatched.
var ErrInvalidRequest = errors.New("invalid driver request")

// ErrHandInTooLarge reports a change too large to hand in as evidence.
var ErrHandInTooLarge = errors.New("hand-in too large")

// Request is one task dispatched to one worker in one worktree.
type Request struct {
	Task marshal.Task
	// Worktree is the task's git worktree; the worker runs there and nowhere else.
	Worktree string
	// Brief is the instruction the worker receives.
	Brief string
	// Model selects the worker's model; empty leaves the CLI's default.
	Model string
}

func (r Request) validate() error {
	switch {
	case strings.TrimSpace(r.Task.PlanTaskID) == "":
		return fmt.Errorf("%w: task has no id", ErrInvalidRequest)
	case strings.TrimSpace(r.Task.BaseCommit) == "":
		return fmt.Errorf("%w: task %s has no base commit", ErrInvalidRequest, r.Task.PlanTaskID)
	case strings.TrimSpace(r.Worktree) == "":
		return fmt.Errorf("%w: task %s has no worktree", ErrInvalidRequest, r.Task.PlanTaskID)
	case strings.TrimSpace(r.Brief) == "":
		return fmt.Errorf("%w: task %s has no brief", ErrInvalidRequest, r.Task.PlanTaskID)
	}
	return nil
}

// Handle is a launched task. It is owned by the driver that returned it.
type Handle struct {
	req    Request
	cancel context.CancelFunc
	done   chan struct{}

	// Set before done is closed and read only after it.
	observed marshal.CommandRecord
	reported []marshal.CommandRecord
	runErr   error
}

func (h *Handle) Worktree() string { return h.req.Worktree }

// Request returns the original request for this handle.
func (h *Handle) Request() Request {
	return h.req
}

// NewHandle creates a handle for a given request.
func NewHandle(req Request) *Handle {
	return &Handle{req: req, done: make(chan struct{})}
}

// SetObserved records the runtime-observed command record on the handle.
func (h *Handle) SetObserved(rec marshal.CommandRecord) {
	h.observed = rec
}

// SetReported records the worker-reported actions on the handle.
func (h *Handle) SetReported(recs []marshal.CommandRecord) {
	h.reported = recs
}

// SetRunErr records the execution error on the handle.
func (h *Handle) SetRunErr(err error) {
	h.runErr = err
}

// SetCancel binds the cancellation function to the handle.
func (h *Handle) SetCancel(cancel context.CancelFunc) {
	h.cancel = cancel
}

// Complete marks the handle as done by closing its done channel.
func (h *Handle) Complete() {
	select {
	case <-h.done:
	default:
		close(h.done)
	}
}

// Done returns the channel that closes when the handle finishes.
func (h *Handle) Done() <-chan struct{} {
	return h.done
}

// WorkerCommander is an optional interface implemented by drivers that can
// describe the worker command line, arguments, and environment.
type WorkerCommander interface {
	WorkerCommand(req Request) (binary string, args []string, env []string)
}

// OutputParser is an optional interface implemented by drivers that can parse
// worker-reported actions from output.
type OutputParser interface {
	ParseOutput(stream []byte) []marshal.CommandRecord
}

// Driver runs tasks on one kind of worker.
type Driver interface {
	Mode() marshal.WorkerMode
	// Launch starts the task and returns at once.
	Launch(ctx context.Context, req Request) (*Handle, error)
	// Wait blocks until the worker exits, then assembles the hand-in.
	// Cancelling ctx cancels the worker.
	Wait(ctx context.Context, h *Handle) (marshal.HandIn, error)
	// Cancel stops the worker. The worktree and anything the worker wrote
	// stay in place, so the task can be inspected or resumed.
	Cancel(h *Handle) error
}

// identity names who produced a hand-in.
type identity struct {
	worker   string
	provider string
	model    string
	mode     marshal.WorkerMode
}

// wait blocks on the handle and cancels the worker if ctx ends first.
func wait(ctx context.Context, h *Handle) error {
	select {
	case <-h.done:
		return nil
	case <-ctx.Done():
		h.cancel()
		<-h.done
		return ctx.Err()
	}
}

// assemble builds the hand-in from git and from checks the runtime re-runs.
// Nothing in it is taken from the worker except the reported actions, which
// are kept apart and never count as acceptance evidence.
func assemble(ctx context.Context, req Request, id identity, observed, reported []marshal.CommandRecord, checkTimeout time.Duration) (marshal.HandIn, error) {
	wt := req.Worktree
	result, err := recordResult(ctx, wt, req.Task.PlanTaskID)
	if err != nil {
		return marshal.HandIn{}, err
	}
	diff, err := git(ctx, wt, "diff", "--no-ext-diff", "--no-textconv", req.Task.BaseCommit, result)
	if err != nil {
		return marshal.HandIn{}, err
	}
	names, err := git(ctx, wt, "diff", "--no-ext-diff", "--no-textconv", "--name-only", req.Task.BaseCommit, result)
	if err != nil {
		return marshal.HandIn{}, err
	}
	if checkTimeout <= 0 {
		checkTimeout = DefaultCheckTimeout
	}
	var checks []marshal.CheckResult
	for _, c := range req.Task.Checks {
		record := runCheck(ctx, wt, result, c.Command, checkTimeout)
		observed = append(observed, record)
		checks = append(checks, marshal.CheckResult{
			Command:      c.Command,
			Criteria:     append([]string(nil), c.Criteria...),
			Passed:       record.ExitCode == 0,
			ResultCommit: result,
		})
	}
	return marshal.HandIn{
		BaseCommit:      req.Task.BaseCommit,
		ResultCommit:    result,
		Diff:            diff,
		FilesTouched:    lines(names),
		Worker:          id.worker,
		Provider:        id.provider,
		Model:           id.model,
		Mode:            id.mode,
		RuntimeObserved: observed,
		WorkerReported:  reported,
		CheckResults:    checks,
	}, nil
}

// recordResult commits whatever the worker left in the worktree and returns
// the result commit. The commit uses the repository's own identity: a
// runtime that invented an author would falsify who made the change.
func recordResult(ctx context.Context, wt, taskID string) (string, error) {
	status, err := git(ctx, wt, "status", "--porcelain=v1", "--untracked-files=normal")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(status) != "" {
		for _, key := range []string{"user.name", "user.email"} {
			if value, err := git(ctx, wt, "config", "--get", key); err != nil || strings.TrimSpace(value) == "" {
				return "", fmt.Errorf("recording the hand-in needs git %s; configure it in the repository or git configuration", key)
			}
		}
		if _, err := git(ctx, wt, "add", "-A"); err != nil {
			return "", err
		}
		if _, err := git(ctx, wt, "commit", "--no-verify", "-m", "marshal: hand-in for "+taskID); err != nil {
			return "", err
		}
	}
	head, err := git(ctx, wt, "rev-parse", "HEAD")
	return strings.TrimSpace(head), err
}

// CheckRunner receives the detached checkout and approved command. Runtime
// composition supplies a sandboxed runner; native sessions retain their runner.
type CheckRunner func(context.Context, string, string) marshal.CommandRecord
type checkRunnerKey struct{}
type checkRunnerBinding struct{ run CheckRunner }

func WithCheckRunner(ctx context.Context, run CheckRunner) context.Context {
	return context.WithValue(ctx, checkRunnerKey{}, checkRunnerBinding{run})
}

// runCheck runs one approved check against the result commit and records it.
//
// Each check gets its own clean, detached checkout of the result, removed
// afterwards. Running in the task worktree would let one check change what
// the next one sees while both results still claim the same commit.
func runCheck(ctx context.Context, wt, result, command string, timeout time.Duration) marshal.CommandRecord {
	binding, governed := ctx.Value(checkRunnerKey{}).(checkRunnerBinding)
	if governed && binding.run == nil {
		return marshal.CommandRecord{Command: command, ExitCode: -1, Output: "governed check sandbox runner unavailable"}
	}
	dir, err := os.MkdirTemp("", "marshal-check-")
	if err != nil {
		return marshal.CommandRecord{Command: command, ExitCode: -1, Output: "prepare check checkout: " + err.Error()}
	}
	defer os.RemoveAll(dir)
	checkout := filepath.Join(dir, "tree")
	if _, err := git(ctx, wt, "worktree", "add", "--detach", checkout, result); err != nil {
		return marshal.CommandRecord{Command: command, ExitCode: -1, Output: "prepare check checkout: " + err.Error()}
	}
	defer func() { _, _ = git(context.WithoutCancel(ctx), wt, "worktree", "remove", "--force", checkout) }()

	if governed {
		checkCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		return binding.run(checkCtx, checkout, command)
	}
	process, err := worker.RunVerification(ctx, checkout, []string{"/bin/sh", "-c", command}, timeout, maxOutput)
	code := process.ExitCode
	if err != nil {
		code = -1
	}
	output := string(process.Stdout) + string(process.Stderr)
	if err != nil {
		output += "\ncheck failed: " + err.Error()
	}
	return marshal.CommandRecord{Command: command, ExitCode: code, Output: output}
}

// capBuffer keeps the first limit bytes written to it and counts the rest.
// Output is bounded while it is produced, so a process that prints without
// end cannot exhaust memory before it exits.
type capBuffer struct {
	limit   int
	buf     bytes.Buffer
	dropped int64
}

func (b *capBuffer) Write(p []byte) (int, error) {
	if room := b.limit - b.buf.Len(); room > 0 {
		if len(p) <= room {
			b.buf.Write(p)
		} else {
			b.buf.Write(p[:room])
			b.dropped += int64(len(p) - room)
		}
	} else {
		b.dropped += int64(len(p))
	}
	return len(p), nil
}

// Bytes returns what was kept.
func (b *capBuffer) Bytes() []byte { return b.buf.Bytes() }

// String returns what was kept and states how much was dropped.
func (b *capBuffer) String() string {
	if b.dropped == 0 {
		return b.buf.String()
	}
	return b.buf.String() + fmt.Sprintf("\n[truncated: %d bytes dropped]", b.dropped)
}

// git runs git in dir. Its output is bounded while it is produced; output
// past the limit is an error rather than a silently shortened diff, because
// a hand-in whose evidence was cut would misstate what the worker changed.
func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd, err := hostgit.Command(ctx, dir, args...)
	if err != nil {
		return "", err
	}
	stdout := &capBuffer{limit: maxGitOutput}
	stderr := &capBuffer{limit: maxOutput}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	if stdout.dropped > 0 {
		return "", fmt.Errorf("%w: git %s produced more than %d bytes", ErrHandInTooLarge, strings.Join(args, " "), maxGitOutput)
	}
	return stdout.buf.String(), nil
}

func lines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// Bound keeps the head of an output and says how much was dropped.
func Bound(s string) string {
	return bound(s)
}

// bound keeps the head of an output and says how much was dropped.
func bound(s string) string {
	if len(s) <= maxOutput {
		return s
	}
	return s[:maxOutput] + fmt.Sprintf("\n[truncated: %d of %d bytes kept]", maxOutput, len(s))
}

// GovernedRunner runs one task through Process 05 in preserve-branch mode and
// returns the actions it recorded. The runtime wires it to the execution
// engine; the driver only needs the outcome.
type GovernedRunner func(ctx context.Context, req Request) ([]marshal.CommandRecord, error)

// Governed runs tasks through Process 05, where every action already passes
// MARSHAL policy. Its hand-in is assembled exactly like a native one.
type Governed struct {
	Check        func(context.Context, Request, string, string) marshal.CommandRecord
	Run          GovernedRunner
	Provider     string
	CheckTimeout time.Duration
}

func (Governed) Mode() marshal.WorkerMode { return marshal.Governed }

func (g Governed) WorkerCommand(req Request) (string, []string, []string) {
	provider := g.Provider
	if provider == "" {
		provider = req.Task.Worker
	}
	switch provider {
	case "codex":
		d := Codex("")
		return d.Binary, d.Args(req), cleanWorkerEnv(os.Environ())
	case "claude", "claude-code":
		d := Claude("")
		return d.Binary, d.Args(req), cleanWorkerEnv(os.Environ())
	case "agy", "antigravity":
		d := Agy("")
		return d.Binary, d.Args(req), cleanWorkerEnv(os.Environ())
	case "opencode":
		d := OpenCode("")
		return d.Binary, d.Args(req), cleanWorkerEnv(os.Environ())
	default:
		return provider, nil, cleanWorkerEnv(os.Environ())
	}
}

// Launch starts the governed run.
func (g Governed) Launch(ctx context.Context, req Request) (*Handle, error) {
	if err := req.validate(); err != nil {
		return nil, err
	}
	if g.Run == nil {
		return nil, fmt.Errorf("%w: governed driver has no runner", ErrInvalidRequest)
	}
	runCtx, cancel := context.WithCancel(ctx)
	h := &Handle{req: req, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(h.done)
		h.reported, h.runErr = g.Run(runCtx, req)
		h.observed = marshal.CommandRecord{Command: "process05 " + req.Task.PlanTaskID}
		if h.runErr != nil {
			h.observed.ExitCode = 1
			h.observed.Output = bound(h.runErr.Error())
		}
	}()
	return h, nil
}

// Wait assembles the hand-in once the governed run ends.
func (g Governed) Wait(ctx context.Context, h *Handle) (marshal.HandIn, error) {
	if err := wait(ctx, h); err != nil {
		return marshal.HandIn{}, err
	}
	if h.runErr != nil {
		return marshal.HandIn{}, h.runErr
	}
	id := identity{worker: h.req.Task.Worker, provider: g.Provider, model: h.req.Model, mode: marshal.Governed}
	var checks CheckRunner
	if g.Check != nil {
		checks = func(ctx context.Context, dir, command string) marshal.CommandRecord {
			return g.Check(ctx, h.req, dir, command)
		}
	}
	return assemble(WithCheckRunner(ctx, checks), h.req, id, []marshal.CommandRecord{h.observed}, h.reported, g.CheckTimeout)
}

// Cancel stops the governed run; the worktree is left as it is.
func (g Governed) Cancel(h *Handle) error {
	h.cancel()
	<-h.done
	return nil
}
