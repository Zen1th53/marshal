package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/sandbox"
	"github.com/Zen1th53/marshal/internal/worker"
)

// Checks have their own visible run identity linked to the plan run, with no
// default network grants. The existing local operator command is the only
// path that can change their allowlist while a check is running.
func (r *Runtime) runGovernedCheck(ctx context.Context, parent, taskID, subject, dir, command string) marshal.CommandRecord {
	record := marshal.CommandRecord{Command: command, ExitCode: -1}
	fail := func(err error) marshal.CommandRecord {
		if sandbox.UnconfinedFallbackAllowed() {
			return r.runCheckUnconfined(ctx, taskID, subject, dir, command, err)
		}
		record.Output = err.Error()
		return record
	}
	if sandbox.HostExecutionPreferred() {
		return r.runCheckUnconfined(ctx, taskID, subject, dir, command, errHostExecution)
	}
	if !r.egressEnforcementAvailable() {
		return fail(fmt.Errorf("check socket confinement unavailable"))
	}
	id, err := model.NewID("RUN-CHECK-")
	if err != nil {
		return fail(err)
	}
	socket, cleanup, err := r.startRunEgress(ctx, id, parent, "check", taskID, subject, nil)
	if err != nil {
		return fail(err)
	}
	defer cleanup()
	bwrap, err := trustedBwrapPath()
	if err != nil {
		return fail(err)
	}
	bridge, err := sandbox.TrustedBridgePath()
	if err != nil {
		return fail(err)
	}
	request, err := worker.VerificationSandboxRequest(ctx, dir)
	if err != nil {
		return fail(err)
	}
	request.NetworkAllowed, request.EgressSocket, request.BridgeBinary = true, socket, bridge
	runner := worker.NewObservedSandboxed(worker.New(driver.DefaultCheckTimeout, time.Second, 64<<10), sandbox.NewBwrap(bwrap), request, r.socketObserver(socket))
	result, err := runner.Run(ctx, adapter.Command{Path: "/bin/sh", Args: []string{"-c", command}})
	if err != nil {
		return fail(err)
	}
	if err := worker.VerificationResultError(result); err != nil {
		if sandbox.UnconfinedFallbackAllowed() && !result.TimedOut && !result.Cancelled && !result.OutputTruncated {
			return fail(err)
		}
		record.Output = err.Error()
		return record
	}
	record.ExitCode = result.ExitCode
	record.Output = string(result.Stdout) + string(result.Stderr)
	return record
}
func (r *Runtime) governedHandInCheck(ctx context.Context, req driver.Request, dir, command string) marshal.CommandRecord {
	parent := ""
	if parts := strings.Split(req.Task.Branch, "/"); len(parts) == 3 {
		parent = parts[1]
	}
	return r.runGovernedCheck(ctx, parent, req.Task.PlanTaskID, req.Task.Worker, dir, command)
}

// runCheckUnconfined runs a check on the host after confinement refused it
// (Marshal build only) and says so in the recorded output and the event log.
func (r *Runtime) runCheckUnconfined(ctx context.Context, taskID, subject, dir, command string, cause error) marshal.CommandRecord {
	result, runErr := worker.New(driver.DefaultCheckTimeout, time.Second, 64<<10).Run(ctx, adapter.Command{Path: "/bin/sh", Args: []string{"-c", command}, Dir: dir})
	r.recordUnconfined(ctx, taskID, subject, cause)
	record := marshal.CommandRecord{Command: command, ExitCode: result.ExitCode}
	record.Output = string(result.Stdout) + string(result.Stderr) + fmt.Sprintf("\nmarshal-evidence: ran unconfined on the host (confinement refused: %v)", cause)
	if runErr != nil {
		record.ExitCode = -1
		record.Output += fmt.Sprintf("; run error: %v", runErr)
	}
	return record
}
