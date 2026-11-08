//go:build !governed

package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/project"
)

const (
	// agySigninKeepaliveInterval is safely below agy's roughly one-hour sign-in
	// lifetime, so a live worker's sign-in is refreshed before it expires.
	agySigninKeepaliveInterval = 45 * time.Minute
	// agySigninKeepaliveScanInterval is how often the runtime checks whether a
	// refresh is due.
	agySigninKeepaliveScanInterval = 5 * time.Minute
	// agySigninKeepaliveTimeout bounds one minimal headless refresh call.
	agySigninKeepaliveTimeout = 30 * time.Second
	// agySigninKeepalivePrompt is the trivial prompt that makes agy refresh its
	// own sign-in without doing any work.
	agySigninKeepalivePrompt = "ok"
	// agySigninKeepaliveID names the synthetic task in the existing owner notice.
	agySigninKeepaliveID = "agy-signin-keepalive"
	// agySigninRefreshEvent records a successful refresh.
	agySigninRefreshEvent = "AGY_SIGNIN_REFRESHED"
	// agySigninRefreshFailedEvent records a failed refresh with a bounded reason.
	agySigninRefreshFailedEvent = "AGY_SIGNIN_REFRESH_FAILED"
)

// agySigninKeepaliveNow is the scheduling clock. Tests replace it.
var agySigninKeepaliveNow = time.Now

// agySigninKeepaliveState is process-wide like the sign-in it refreshes.
var agySigninKeepaliveState struct {
	sync.Mutex
	lastSuccess time.Time
	inFlight    bool
}

// startAgySigninKeepaliveLoop follows the bounded re-verify runner: one
// lifecycle owner periodically decides whether a refresh is due. Nothing is
// called while agy is not a worker of a live run.
func (r *Runtime) startAgySigninKeepaliveLoop(ctx context.Context) {
	if r == nil || r.store == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(agySigninKeepaliveScanInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !r.IsLifecycleOwner() {
					return
				}
				r.keepAgySigninFresh(ctx)
			}
		}
	}()
}

// keepAgySigninFresh refreshes only while agy is a worker of a live run.
func (r *Runtime) keepAgySigninFresh(ctx context.Context) {
	if r == nil || r.store == nil {
		return
	}
	if !r.agySigninLiveWorker(ctx) {
		return
	}
	r.refreshAgySigninIfStale(ctx, agySigninKeepaliveNow().UTC())
}

// refreshAgySigninBeforeDispatch refreshes a stale sign-in before an agy task
// starts, even when the periodic loop has not reached its next tick.
func (r *Runtime) refreshAgySigninBeforeDispatch(ctx context.Context) {
	if r == nil || r.store == nil {
		return
	}
	r.refreshAgySigninIfStale(ctx, agySigninKeepaliveNow().UTC())
}

// refreshAgySigninIfStale admits at most one keep-alive call at a time and only
// when the last success is older than the keep-alive interval. A zero last
// success counts as stale, so a fresh process establishes freshness once.
func (r *Runtime) refreshAgySigninIfStale(ctx context.Context, now time.Time) {
	if !r.agySigninUsesHostCLI() {
		return
	}
	state := &agySigninKeepaliveState
	state.Lock()
	if state.inFlight {
		state.Unlock()
		return
	}
	if !state.lastSuccess.IsZero() && now.Sub(state.lastSuccess) < agySigninKeepaliveInterval {
		state.Unlock()
		return
	}
	state.inFlight = true
	state.Unlock()
	defer func() {
		state.Lock()
		state.inFlight = false
		state.Unlock()
	}()
	r.refreshAgySignin(ctx, now)
}

// refreshAgySignin runs one minimal headless agy call. It never reads or writes
// a credential, token or sign-in file: agy refreshes its own sign-in. The call
// runs in a disposable directory so the workspace is never changed.
func (r *Runtime) refreshAgySignin(ctx context.Context, now time.Time) {
	binary, err := project.FindBinary("agy")
	if err != nil {
		r.recordAgySigninRefresh(ctx, false, "agy CLI is unavailable")
		return
	}
	dir, err := os.MkdirTemp("", "marshal-agy-keepalive-")
	if err != nil {
		r.recordAgySigninRefresh(ctx, false, "keep-alive workspace is unavailable")
		return
	}
	defer os.RemoveAll(dir)

	callCtx, cancel := context.WithTimeout(ctx, agySigninKeepaliveTimeout)
	defer cancel()
	started := time.Now().UTC()
	cmd := exec.CommandContext(callCtx, binary, "--print", agySigninKeepalivePrompt)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	exitCode := 0
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}
	elapsed := time.Since(started)
	result := adapter.Result{
		ExitCode: exitCode, Stdout: stdout.Bytes(), Stderr: stderr.Bytes(),
		StartedAt: started, EndedAt: started.Add(elapsed),
	}
	if runErr == nil && exitCode == 0 {
		agySigninKeepaliveState.Lock()
		agySigninKeepaliveState.lastSuccess = now
		agySigninKeepaliveState.Unlock()
		clearAgySigninExpiryCooldown(r)
		r.recordAgySigninRefresh(ctx, true, "")
		return
	}
	if classifyAgySigninExpiry(runErr, exitCode, stdout.Bytes(), stderr.Bytes(), elapsed) {
		// Only the sign-in-expiry classification applies the existing cooldown
		// and its single owner notice.
		_, _ = r.noteAgySigninExpiry(context.WithoutCancel(ctx), execution.TaskExecution{TaskID: agySigninKeepaliveID}, runErr, result)
		r.recordAgySigninRefresh(ctx, false, agySigninExpiryReason)
		return
	}
	r.recordAgySigninRefresh(ctx, false, agySigninRefreshReason(runErr, exitCode))
}

// agySigninUsesHostCLI reports whether agy workers run the host's agy CLI.
// A runtime given its own agy adapter never launches that CLI, so the host
// sign-in is not its workers' sign-in: refreshing it would call the real
// provider and could cool agy down for a failure that is not the workers'.
func (r *Runtime) agySigninUsesHostCLI() bool {
	return r != nil && r.adapters["agy"] == nil && r.adapters["antigravity"] == nil
}

// clearAgySigninExpiryCooldown ends an active sign-in episode after a
// successful refresh, so agy is offered again.
func clearAgySigninExpiryCooldown(r *Runtime) {
	agySigninEpisode.Lock()
	active := !agySigninEpisode.until.IsZero() && time.Now().Before(agySigninEpisode.until)
	agySigninEpisode.until = time.Time{}
	agySigninEpisode.Unlock()
	if active && r != nil {
		r.clearProviderLimit("agy")
	}
}

// agySigninLiveWorker reports whether any unfinished Process 05 run still holds
// an agy worker. The keep-alive must do nothing otherwise.
func (r *Runtime) agySigninLiveWorker(ctx context.Context) bool {
	service := r.Execution()
	if service == nil {
		return false
	}
	runs, err := service.ListRuns(ctx)
	if err != nil {
		return false
	}
	for _, run := range runs {
		if run.State == execution.RunReady || run.State.IsTerminal() {
			continue
		}
		for _, task := range run.Tasks {
			if normalizeProvider(task.AssignedHarness) != "agy" {
				continue
			}
			if task.State.IsTerminal() {
				continue
			}
			return true
		}
	}
	return false
}

// recordAgySigninRefresh persists one outcome of the keep-alive. The reason is
// bounded and never carries provider output, so no credential can leak into it.
func (r *Runtime) recordAgySigninRefresh(ctx context.Context, success bool, reason string) {
	if r == nil || r.store == nil {
		return
	}
	ctx = context.WithoutCancel(ctx)
	id, err := model.NewID("EVENT-")
	if err != nil {
		return
	}
	projectRecord, err := r.store.Project(ctx)
	if err != nil {
		return
	}
	eventType := agySigninRefreshFailedEvent
	outcome := "failure"
	if success {
		eventType = agySigninRefreshEvent
		outcome = "success"
	}
	data := map[string]any{"provider": "agy", "outcome": outcome, "source": "agy-signin-keepalive"}
	if reason != "" {
		data["reason"] = agySigninBoundedReason(reason)
	}
	_ = r.store.AppendEvent(ctx, nil, model.Event{
		ID: id, Type: eventType, ProjectID: projectRecord.ID, Timestamp: time.Now().UTC(), Data: data,
	})
}

func agySigninRefreshReason(runErr error, exitCode int) string {
	if runErr == nil {
		return fmt.Sprintf("exit code %d", exitCode)
	}
	reason := strings.TrimSpace(runErr.Error())
	if reason == "" {
		return fmt.Sprintf("exit code %d", exitCode)
	}
	return agySigninBoundedReason(reason)
}

func agySigninBoundedReason(reason string) string {
	const limit = 160
	reason = strings.TrimSpace(reason)
	if len(reason) > limit {
		reason = reason[:limit]
	}
	return reason
}
