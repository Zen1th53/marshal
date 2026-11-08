//go:build !governed

package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/adapter/antigravity"
	"github.com/Zen1th53/marshal/internal/execution"
)

func resetAgySigninKeepalive(t *testing.T) {
	t.Helper()
	ResetProviderCooldowns()
	agySigninEpisode.Lock()
	agySigninEpisode.until = time.Time{}
	agySigninEpisode.Unlock()
	agySigninKeepaliveState.Lock()
	agySigninKeepaliveState.lastSuccess = time.Time{}
	agySigninKeepaliveState.inFlight = false
	agySigninKeepaliveState.Unlock()
	previous := agySigninKeepaliveNow
	agySigninKeepaliveNow = time.Now
	t.Cleanup(func() {
		agySigninKeepaliveNow = previous
		ResetProviderCooldowns()
		agySigninEpisode.Lock()
		agySigninEpisode.until = time.Time{}
		agySigninEpisode.Unlock()
		agySigninKeepaliveState.Lock()
		agySigninKeepaliveState.lastSuccess = time.Time{}
		agySigninKeepaliveState.inFlight = false
		agySigninKeepaliveState.Unlock()
	})
}

// agySigninKeepaliveFixture installs a fake agy on PATH that records every
// invocation and never touches a real credential. It returns the call log and
// the script path so a test can switch the outcome.
func agySigninKeepaliveFixture(t *testing.T, failure bool) (callsPath, scriptPath string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	bin := t.TempDir()
	callsPath = filepath.Join(home, "agy-calls.log")
	body := "#!/bin/sh\nprintf '%s\\n' \"$0 $*\" >> \"" + callsPath + "\"\n"
	if failure {
		body += "printf '%s\\n' 'Error: sign-in token expired, please sign in again' >&2\nexit 1\n"
	} else {
		body += "printf '%s\\n' 'ok'\nexit 0\n"
	}
	scriptPath = filepath.Join(bin, "agy")
	if err := os.WriteFile(scriptPath, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1")
	return callsPath, scriptPath
}

func agySigninKeepaliveCalls(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

func agySigninKeepaliveEventCount(t *testing.T, r *Runtime, eventType string) int {
	t.Helper()
	history, err := r.store.ListEvents(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, event := range history {
		if event.Type == eventType {
			count++
		}
	}
	return count
}

func createAgySigninKeepaliveLiveRun(t *testing.T, r *Runtime, runID string) {
	t.Helper()
	run := execution.ExecutionRun{
		RunID: runID,
		State: execution.RunRunning,
		Tasks: map[string]execution.TaskExecution{
			"task-1": {RunID: runID, TaskID: "task-1", AssignedHarness: "agy", State: execution.TaskRunning},
		},
	}
	if err := r.Execution().Engine().RunStore().CreateRun(t.Context(), run); err != nil {
		t.Fatal(err)
	}
}

func setAgySigninKeepaliveLastSuccess(now time.Time, age time.Duration) {
	agySigninKeepaliveState.Lock()
	agySigninKeepaliveState.lastSuccess = now.Add(-age)
	agySigninKeepaliveState.Unlock()
}

func TestAgySigninKeepaliveRefreshRunsBeforeExpiry(t *testing.T) {
	resetAgySigninKeepalive(t)
	callsPath, _ := agySigninKeepaliveFixture(t, false)
	r := openNetpolRuntime(t)
	createAgySigninKeepaliveLiveRun(t, r, "RUN-AGY-KEEPALIVE-INTERVAL")

	if agySigninKeepaliveInterval >= agySigninExpiryCooldown {
		t.Fatalf("keep-alive interval %v is not safely below the sign-in expiry %v", agySigninKeepaliveInterval, agySigninExpiryCooldown)
	}
	now := time.Now().UTC()
	agySigninKeepaliveNow = func() time.Time { return now }
	setAgySigninKeepaliveLastSuccess(now, agySigninKeepaliveInterval+time.Minute)

	r.keepAgySigninFresh(t.Context())

	calls := agySigninKeepaliveCalls(t, callsPath)
	if len(calls) != 1 || !strings.Contains(calls[0], "--print") {
		t.Fatalf("agy refresh calls = %v, want one headless call", calls)
	}
	if got := agySigninKeepaliveEventCount(t, r, agySigninRefreshEvent); got != 1 {
		t.Fatalf("success events = %d, want 1", got)
	}
	agySigninKeepaliveState.Lock()
	lastSuccess := agySigninKeepaliveState.lastSuccess
	agySigninKeepaliveState.Unlock()
	if !lastSuccess.Equal(now) {
		t.Fatalf("last success = %v, want %v", lastSuccess, now)
	}

	// A second decision before the interval elapses makes no new call.
	r.keepAgySigninFresh(t.Context())
	if calls := agySigninKeepaliveCalls(t, callsPath); len(calls) != 1 {
		t.Fatalf("a fresh sign-in was refreshed again: %v", calls)
	}
}

func TestAgySigninKeepaliveRefreshesStaleSigninBeforeDispatch(t *testing.T) {
	resetAgySigninKeepalive(t)
	callsPath, _ := agySigninKeepaliveFixture(t, false)
	r := openNetpolRuntime(t)

	now := time.Now().UTC()
	agySigninKeepaliveNow = func() time.Time { return now }
	setAgySigninKeepaliveLastSuccess(now, agySigninKeepaliveInterval+time.Minute)

	r.refreshAgySigninBeforeDispatch(t.Context())

	calls := agySigninKeepaliveCalls(t, callsPath)
	if len(calls) != 1 || !strings.Contains(calls[0], "--print") {
		t.Fatalf("pre-dispatch refresh calls = %v, want one headless call", calls)
	}
	if got := agySigninKeepaliveEventCount(t, r, agySigninRefreshEvent); got != 1 {
		t.Fatalf("success events = %d, want 1", got)
	}
}

func TestAgySigninKeepaliveFailureUsesCooldownAndOneNotice(t *testing.T) {
	resetAgySigninKeepalive(t)
	callsPath, scriptPath := agySigninKeepaliveFixture(t, true)
	r := openNetpolRuntime(t)
	createAgySigninKeepaliveLiveRun(t, r, "RUN-AGY-KEEPALIVE-FAIL")

	now := time.Now().UTC()
	agySigninKeepaliveNow = func() time.Time { return now }
	setAgySigninKeepaliveLastSuccess(now, agySigninKeepaliveInterval+time.Minute)

	r.keepAgySigninFresh(t.Context())

	if !isProviderUnavailable("agy", time.Now()) {
		t.Fatal("a classified refresh failure did not put agy into its sign-in cooldown")
	}
	if n := agySigninExpiryNotices(t, r); n != 1 {
		t.Fatalf("owner notices = %d, want 1", n)
	}
	if n := agySigninKeepaliveEventCount(t, r, agySigninRefreshFailedEvent); n != 1 {
		t.Fatalf("failure events = %d, want 1", n)
	}
	if n := agySigninKeepaliveEventCount(t, r, agySigninRefreshEvent); n != 0 {
		t.Fatalf("success events = %d, want 0", n)
	}

	// A second failure in the same episode records no second owner notice.
	setAgySigninKeepaliveLastSuccess(now, agySigninKeepaliveInterval+time.Minute)
	r.keepAgySigninFresh(t.Context())
	if n := agySigninExpiryNotices(t, r); n != 1 {
		t.Fatalf("owner notices = %d after a repeated failure, want 1", n)
	}

	// A later successful refresh clears the sign-in cooldown.
	success := "#!/bin/sh\nprintf '%s\\n' \"$0 $*\" >> \"" + callsPath + "\"\nprintf '%s\\n' 'ok'\nexit 0\n"
	if err := os.WriteFile(scriptPath, []byte(success), 0700); err != nil {
		t.Fatal(err)
	}
	setAgySigninKeepaliveLastSuccess(now, agySigninKeepaliveInterval+time.Minute)
	r.keepAgySigninFresh(t.Context())

	if isProviderUnavailable("agy", time.Now()) {
		t.Fatal("a successful refresh did not clear the sign-in cooldown")
	}
	if n := agySigninKeepaliveEventCount(t, r, agySigninRefreshEvent); n != 1 {
		t.Fatalf("success events = %d, want 1", n)
	}
}

func TestAgySigninKeepaliveNeverTouchesCredentials(t *testing.T) {
	resetAgySigninKeepalive(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	configDir := filepath.Join(home, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	tokenPath := filepath.Join(configDir, "antigravity-oauth-token")
	const token = "sentinel-token-value"
	if err := os.WriteFile(tokenPath, []byte(token), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(tokenPath)
	if err != nil {
		t.Fatal(err)
	}

	bin := t.TempDir()
	callsPath := filepath.Join(home, "agy-calls.log")
	scriptPath := filepath.Join(bin, "agy")
	script := "#!/bin/sh\nprintf '%s\\n' \"$0 $*\" >> \"" + callsPath + "\"\nprintf '%s\\n' 'ok'\nexit 0\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1")

	r := openNetpolRuntime(t)
	now := time.Now().UTC()
	agySigninKeepaliveNow = func() time.Time { return now }
	setAgySigninKeepaliveLastSuccess(now, agySigninKeepaliveInterval+time.Minute)

	r.refreshAgySigninBeforeDispatch(t.Context())

	calls := agySigninKeepaliveCalls(t, callsPath)
	if len(calls) != 1 || !strings.HasPrefix(calls[0], scriptPath) {
		t.Fatalf("invocations = %v, want only the fake agy at %s", calls, scriptPath)
	}
	data, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatalf("read sentinel token after refresh: %v", err)
	}
	if string(data) != token {
		t.Fatalf("credential content changed: %q", data)
	}
	after, err := os.Stat(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("the credential file was rewritten by the keep-alive")
	}
}

// agySigninKeepaliveNoRunner backs an injected agy adapter that must never run.
type agySigninKeepaliveNoRunner struct{}

func (agySigninKeepaliveNoRunner) Run(context.Context, adapter.Command) (adapter.ProcessResult, error) {
	return adapter.ProcessResult{}, errors.New("injected agy adapter must not run")
}

func TestAgySigninKeepaliveSkipsRuntimeWithOwnAgyAdapter(t *testing.T) {
	resetAgySigninKeepalive(t)
	callsPath, _ := agySigninKeepaliveFixture(t, true)
	repo := runtimeRepo(t)
	if _, err := Bootstrap(t.Context(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	r, err := OpenWithOptions(t.Context(), repo.Path(), Options{Adapters: map[string]adapter.Adapter{"agy": antigravity.NewGoverned("/prepared/agy", agySigninKeepaliveNoRunner{})}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	createAgySigninKeepaliveLiveRun(t, r, "RUN-AGY-KEEPALIVE-OWN-ADAPTER")
	now := time.Now().UTC()
	agySigninKeepaliveNow = func() time.Time { return now }
	setAgySigninKeepaliveLastSuccess(now, agySigninKeepaliveInterval+time.Minute)

	r.refreshAgySigninBeforeDispatch(t.Context())
	r.keepAgySigninFresh(t.Context())

	if calls := agySigninKeepaliveCalls(t, callsPath); len(calls) != 0 {
		t.Fatalf("host agy called for a runtime with its own agy adapter: %v", calls)
	}
	if isProviderUnavailable("agy", time.Now()) {
		t.Fatal("a host sign-in failure cooled down the runtime's own agy workers")
	}
}
