package app

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/adapter/claude"
	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
)

type fixtureRunner struct{ result adapter.ProcessResult }

func (f fixtureRunner) Run(context.Context, adapter.Command) (adapter.ProcessResult, error) {
	return f.result, nil
}

const claudeSigninWarning = "Warning: this sign-in can end at any time from now until 2026-11-06\n"

func claudeProbeFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("../adapter/claude/testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// Reproduces the 2026-11-06 live failure: Claude CLI 2.1.318 with
// "verbose": true prints the probe's events as one JSON array (init,
// assistant, rate_limit_event status=allowed, result success). The probe
// must pass, and nothing in that output may read as a provider limit.
func TestClaudeVerboseProbeOutputPassesAndIsNoLimit(t *testing.T) {
	for _, name := range []string{"probe-verbose-json.json", "probe-stream-json.jsonl"} {
		t.Run(name, func(t *testing.T) {
			stdout := claudeProbeFixture(t, name)
			isolation := model.IsolationCapability{Level: model.IsolationBwrap, Available: true, Filesystem: true, Process: true, Network: true}
			client := claude.New("claude", fixtureRunner{adapter.ProcessResult{Stdout: stdout, Stderr: []byte(claudeSigninWarning), Isolation: isolation}})
			result, err := client.Run(context.Background(), adapter.Request{TaskID: "PROBE-1", Title: "Reply exactly " + harnessProbeReply, Worktree: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			if err := validateHarnessProbe(result); err != nil {
				t.Fatalf("recorded successful probe rejected: %v", err)
			}
			if limit := providerLimitReason(result.Stdout, result.Stderr); limit != "" {
				t.Fatalf("informational output reported as a limit: %q", limit)
			}
			if limited, reason, _ := classifyProviderLimitAt(errors.New("worker failed"), marshal.HandIn{RuntimeObserved: []marshal.CommandRecord{{Command: "worker", ExitCode: 1, Output: string(stdout)}}}, nil, time.Now()); limited {
				t.Fatalf("failover classified an allowed rate_limit_event as a limit: %q", reason)
			}
			r := &Runtime{}
			if r.noteProviderLimit("claude-fixture", string(stdout)+"\n"+claudeSigninWarning) {
				t.Fatal("noteProviderLimit marked an allowed stream as limited")
			}
		})
	}
}

func TestRejectedRateLimitEventNamesTheLimitNotTheJSONLine(t *testing.T) {
	reset := time.Now().Add(2 * time.Hour).Truncate(time.Second).UTC()
	stdout := `[{"type":"system","subtype":"init","tools":["Bash"],"model":"claude-opus-5-5"},` +
		`{"type":"rate_limit_event","rate_limit_info":{"status":"rejected","resetsAt":` + strconv.FormatInt(reset.Unix(), 10) + `,"rateLimitType":"five_hour"}},` +
		`{"type":"result","subtype":"success","is_error":true,"result":"Claude AI usage limit reached"}]`
	got := providerLimitReason([]byte(stdout))
	if !strings.HasPrefix(got, "rate limit rejected (five_hour), resets at "+reset.Format(time.RFC3339)) || strings.Contains(got, `"type"`) {
		t.Fatalf("limit reason = %q", got)
	}
	window, ok := parseCooldownFound(providerLimitText(stdout), time.Now())
	if !ok || window < 110*time.Minute || window > 2*time.Hour {
		t.Fatalf("cooldown = %v %v", window, ok)
	}
	if limited, reason, _ := classifyProviderLimitAt(errors.New("exit 1"), marshal.HandIn{}, nil, time.Now()); limited {
		t.Fatalf("bare error classified as limit: %q", reason)
	}
}

func TestErrorResultTextIsTheLimitReason(t *testing.T) {
	stdout := `{"type":"system","subtype":"init","tools":["RateLimitTool"]}` + "\n" +
		`{"type":"result","subtype":"error_during_execution","is_error":true,"result":"5-hour limit reached ∙ resets 3pm","api_error_status":429}` + "\n"
	if got := providerLimitReason([]byte(stdout)); got != "5-hour limit reached ∙ resets 3pm" {
		t.Fatalf("limit reason = %q", got)
	}
	// A successful result's text is the model's reply, not a limit.
	if got := providerLimitReason([]byte(`{"type":"result","subtype":"success","is_error":false,"result":"I fixed the rate limit handling."}`)); got != "" {
		t.Fatalf("model reply reported as a limit: %q", got)
	}
}

func TestProviderLimitTextKeepsPlainAndEmbeddedText(t *testing.T) {
	if got := providerLimitText("socat[4] W exiting on signal 15"); got != "socat[4] W exiting on signal 15" {
		t.Fatalf("plain text changed: %q", got)
	}
	embedded := `Marshal model turn: exit status 1: [{"type":"system","subtype":"init","rateLimitType":"x"},{"type":"rate_limit_event","rate_limit_info":{"status":"allowed"}}]`
	if got := providerLimitReason([]byte(embedded)); got != "" {
		t.Fatalf("embedded informational JSON reported as a limit: %q", got)
	}
	truncated := `[{"type":"system","subtype":"init","tools":["Task"],"rateLimitType":"five_hour","cwd":"/tmp/x`
	if got := providerLimitReason([]byte(truncated)); got != "" {
		t.Fatalf("truncated init line reported as a limit: %q", got)
	}
	truncatedError := `{"type":"error","message":"Too many requests, retry after 30s","detail":"`
	if got := providerLimitReason([]byte(truncatedError)); got != "Too many requests, retry after 30s" {
		t.Fatalf("truncated error event = %q", got)
	}
}

func TestHarnessProbeFailureNamesTheFailedCheck(t *testing.T) {
	good := adapter.Result{Status: adapter.StatusSuccess, FinalText: harnessProbeReply, Isolation: model.IsolationCapability{Level: model.IsolationBwrap, Available: true, Filesystem: true, Process: true, Network: true}}
	cases := map[string]func(*adapter.Result){
		"output was truncated": func(r *adapter.Result) { r.OutputTruncated = true },
		"exit code 3":          func(r *adapter.Result) { r.ExitCode = 3 },
		"run timed out":        func(r *adapter.Result) { r.TimedOut = true },
		"run was cancelled":    func(r *adapter.Result) { r.Cancelled = true },
		"run status blocked":   func(r *adapter.Result) { r.Status = adapter.StatusBlocked },
		`reply was "nope"`:     func(r *adapter.Result) { r.FinalText = "nope" },
	}
	for want, mutate := range cases {
		result := good
		mutate(&result)
		err := validateHarnessProbe(result)
		if !errors.Is(err, errHarnessProbeReply) || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: %v", want, err)
		}
	}
	long := good
	long.FinalText = strings.Repeat("x", 500)
	if err := validateHarnessProbe(long); err == nil || len(err.Error()) > 220 {
		t.Fatalf("unbounded reply in reason: %v", err)
	}
	sandboxless := good
	sandboxless.Isolation.Network = false
	if err := validateHarnessProbe(sandboxless); err == nil || errors.Is(err, errHarnessProbeReply) {
		t.Fatalf("isolation failure reported as a reply failure: %v", err)
	}
}

func TestMarshalCLIOutputReadsVerboseClaudeArray(t *testing.T) {
	data := `[{"type":"system","subtype":"init","session_id":"s1"},{"type":"rate_limit_event","rate_limit_info":{"status":"allowed"}},{"type":"result","subtype":"success","session_id":"s1","result":"{\"ok\":true}","structured_output":{"ok":true}}]`
	payload, session, err := marshalCLIOutput("claude", []byte(data))
	if err != nil || session != "s1" || string(payload) != `{"ok":true}` {
		t.Fatalf("payload=%s session=%q err=%v", payload, session, err)
	}
}
