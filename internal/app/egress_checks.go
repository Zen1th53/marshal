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
	fail := func(err error) marshal.CommandRecord { record.Output = err.Error(); return record }
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
		return fail(err)
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
