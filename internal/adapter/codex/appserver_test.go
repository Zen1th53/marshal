package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/model"
)

type testAppServerApprovalAuthority struct {
	seen AppServerApprovalRequest
	err  error
}

func (a *testAppServerApprovalAuthority) ApproveAppServerRequest(_ context.Context, request AppServerApprovalRequest) error {
	a.seen = request
	return a.err
}

func TestAppServerThreadStartIsGovernedAndTyped(t *testing.T) {
	req := adapter.Request{
		TaskID:            "TASK-123",
		Title:             "Add governed app-server bridge",
		Worktree:          "/worktree",
		Model:             "gpt-5.6-terra",
		BaseCommit:        "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		HeadCommit:        "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		AllowedOperations: []string{"edit", "test"},
		EvidenceRequired:  []string{"tests"},
	}

	params, err := appServerThreadStartParams(req)
	if err != nil {
		t.Fatalf("thread start params: %v", err)
	}
	encoded, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("decode params: %v", err)
	}
	if got["sandbox"] != "workspace-write" || got["approvalPolicy"] != "untrusted" || got["approvalsReviewer"] != "user" {
		t.Fatalf("unsafe app-server controls: %#v", got)
	}
	if got["ephemeral"] != false || got["cwd"] != req.Worktree || got["model"] != req.Model {
		t.Fatalf("session persistence/binding missing: %#v", got)
	}
	instructions, _ := got["developerInstructions"].(string)
	for _, want := range []string{req.TaskID, req.BaseCommit, req.HeadCommit, "Do not push"} {
		if !strings.Contains(instructions, want) {
			t.Fatalf("developer instructions omit %q: %q", want, instructions)
		}
	}
}

func TestAppServerSessionRejectsNativePolicyDowngrade(t *testing.T) {
	valid := json.RawMessage(`{"thread":{"id":"thread-1","sessionId":"session-1","ephemeral":false},"model":"gpt","cwd":"/worktree","approvalPolicy":"untrusted","approvalsReviewer":"user","sandbox":{"type":"workspace-write"}}`)
	if _, err := appServerSessionFromResponse(valid, "/worktree"); err != nil {
		t.Fatalf("valid governed response rejected: %v", err)
	}
	for _, response := range []json.RawMessage{
		json.RawMessage(`{"thread":{"id":"thread-1","ephemeral":false},"cwd":"/worktree","approvalPolicy":"never","approvalsReviewer":"user","sandbox":{"type":"workspace-write"}}`),
		json.RawMessage(`{"thread":{"id":"thread-1","ephemeral":false},"cwd":"/worktree","approvalPolicy":"untrusted","approvalsReviewer":"auto_review","sandbox":{"type":"workspace-write"}}`),
		json.RawMessage(`{"thread":{"id":"thread-1","ephemeral":false},"cwd":"/worktree","approvalPolicy":"untrusted","approvalsReviewer":"user","sandbox":{"type":"danger-full-access"}}`),
	} {
		if _, err := appServerSessionFromResponse(response, "/worktree"); err == nil {
			t.Fatalf("native policy downgrade accepted: %s", response)
		}
	}
}

// TestAppServerLiveStartSession is deliberately opt-in because it creates a
// real local Codex thread under the caller's Codex profile. It sends no turn
// and does not invoke a model. A blank native thread is not a durable rollout,
// so crash/restart resume is intentionally covered only after a governed turn.
func TestAppServerLiveStartSession(t *testing.T) {
	if os.Getenv("MARSHAL_CODEX_APP_SERVER_E2E") != "1" {
		t.Skip("set MARSHAL_CODEX_APP_SERVER_E2E=1 to exercise installed Codex app-server")
	}
	binary, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("codex is not installed")
	}
	worktree, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	server := NewAppServer(binary, "test")
	req := adapter.Request{
		TaskID: "MARSHAL-APP-SERVER-E2E", Title: "Durable MARSHAL session probe; do not modify files",
		Worktree: worktree, AllowedOperations: []string{"filesystem.read"}, EvidenceRequired: []string{"final report"},
	}
	session, err := server.StartSession(context.Background(), req)
	if err != nil {
		t.Fatalf("start durable session: %v", err)
	}
	if session.ThreadID == "" || !session.Persistent || session.Protocol != appServerProtocolVersion {
		t.Fatalf("invalid started session: %#v", session)
	}
	turn, err := server.StartTurn(context.Background(), session, req)
	if err != nil {
		t.Fatalf("start governed turn: %v", err)
	}
	turnCtx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	result, err := server.WaitTurn(turnCtx, turn)
	var approvalRequired *AppServerApprovalRequiredError
	if errors.As(err, &approvalRequired) {
		if err := server.DeclineApproval(context.Background(), approvalRequired.Request); err != nil {
			t.Fatalf("decline native approval: %v", err)
		}
	} else if err != nil {
		t.Fatalf("wait governed turn: %v", err)
	} else if result.ThreadID != session.ThreadID || result.TurnID != turn.TurnID || result.Status == "" {
		t.Fatalf("invalid terminal app-server turn result: %#v", result)
	}
	if err := server.Close(); err != nil {
		t.Fatalf("close first client: %v", err)
	}
	resumer := NewAppServer(binary, "test")
	defer resumer.Close()
	resumed, err := resumer.ResumeSession(context.Background(), session.ThreadID, req)
	if err != nil {
		t.Fatalf("resume after process restart: %v", err)
	}
	if resumed.ThreadID != session.ThreadID || resumed.Worktree != worktree {
		t.Fatalf("resume binding lost: start=%#v resumed=%#v", session, resumed)
	}
}

func TestRequireAppServerConfigFreeRefusesUserConfiguration(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("sandbox_mode = 'danger-full-access'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RequireAppServerConfigFree(); !errors.Is(err, model.ErrPolicyDenied) {
		t.Fatalf("configured app-server home must be refused: %v", err)
	}
	t.Setenv("MARSHAL_CODEX_APP_SERVER_ALLOW_HOST_CONFIG", "1")
	if err := RequireAppServerConfigFree(); err != nil {
		t.Fatalf("explicit supervised host-config override must be accepted: %v", err)
	}
	t.Setenv("MARSHAL_CODEX_APP_SERVER_ALLOW_HOST_CONFIG", "")
	if err := os.Remove(filepath.Join(home, "config.toml")); err != nil {
		t.Fatal(err)
	}
	if err := RequireAppServerConfigFree(); err != nil {
		t.Fatalf("config-free app-server home must be allowed: %v", err)
	}
}

func TestAppServerThreadStartRejectsIncompleteOrDangerousRequest(t *testing.T) {
	for _, req := range []adapter.Request{
		{},
		{TaskID: "TASK-1", Title: "--dangerously-bypass-approvals-and-sandbox", Worktree: "/worktree"},
		{TaskID: "TASK-1", Title: "valid", Worktree: "--config=unsafe"},
	} {
		if _, err := appServerThreadStartParams(req); err == nil {
			t.Fatalf("expected rejection for %#v", req)
		}
	}
}

func TestAppServerTurnIsBoundToExistingThreadAndHasNoShellPayload(t *testing.T) {
	params, err := appServerTurnStartParams("thread-123", adapter.Request{
		TaskID: "TASK-123", Title: "Make a governed change", Worktree: "/worktree",
	})
	if err != nil {
		t.Fatalf("turn params: %v", err)
	}
	encoded, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	if !strings.Contains(text, `"threadId":"thread-123"`) || !strings.Contains(text, `"type":"text"`) || !strings.Contains(text, "Make a governed change") {
		t.Fatalf("missing typed thread/input binding: %s", text)
	}
	for _, forbidden := range []string{"command/exec", "shellCommand", "danger-full-access"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("turn payload exposed prohibited execution control %q: %s", forbidden, text)
		}
	}
	if !strings.Contains(text, `"approvalPolicy":"untrusted"`) || !strings.Contains(text, `"approvalsReviewer":"user"`) || !strings.Contains(text, `"sandboxPolicy":{"networkAccess":false,"type":"workspaceWrite","writableRoots":["/worktree"]}`) {
		t.Fatalf("turn payload omitted re-injected governed policy: %s", text)
	}
	if _, err := appServerTurnStartParams("", adapter.Request{TaskID: "TASK-123", Title: "valid", Worktree: "/worktree"}); err == nil {
		t.Fatal("missing thread must be rejected")
	}
}

func TestAppServerWaitTurnAcceptsOnlyExactTerminalNotification(t *testing.T) {
	stream := strings.Join([]string{
		`{"method":"turn/completed","params":{"threadId":"other","turn":{"id":"other-turn","status":"completed","items":[]}}}`,
		`{"method":"turn/completed","params":{"threadId":"thread-123","turn":{"id":"turn-123","status":"completed","items":[{"type":"agentMessage","text":"done"}]}}}`,
	}, "\n") + "\n"
	conn := &appServerConnection{stream: appServerStream{in: &strings.Builder{}, out: bufio.NewReader(strings.NewReader(stream))}}
	got, err := conn.waitTurn(context.Background(), AppServerTurn{ThreadID: "thread-123", TurnID: "turn-123"})
	if err != nil {
		t.Fatalf("wait turn: %v", err)
	}
	if got.Status != "completed" || got.FinalText != "done" {
		t.Fatalf("unexpected terminal turn: %#v", got)
	}
}

func TestAppServerWaitTurnAcceptsProtocolTerminalWithoutThreadID(t *testing.T) {
	// The documented app-server turn/completed notification is {turn}; it
	// need not repeat threadId.  The client must correlate it through the
	// exact turn ID returned by turn/start, not hang awaiting an optional
	// redundant field.
	stream := `{"method":"turn/completed","params":{"turn":{"id":"turn-123","status":"completed","items":[{"type":"agentMessage","text":"done"}]}}}` + "\n"
	conn := &appServerConnection{stream: appServerStream{in: &strings.Builder{}, out: bufio.NewReader(strings.NewReader(stream))}}
	got, err := conn.waitTurn(context.Background(), AppServerTurn{ThreadID: "thread-123", TurnID: "turn-123"})
	if err != nil {
		t.Fatalf("wait turn: %v", err)
	}
	if got.ThreadID != "thread-123" || got.TurnID != "turn-123" || got.Status != "completed" || got.FinalText != "done" {
		t.Fatalf("terminal result = %#v", got)
	}
}

func TestAppServerApprovalResponsePreservesNumericJSONRPCID(t *testing.T) {
	requestLine := `{"jsonrpc":"2.0","id":42,"method":"item/commandExecution/requestApproval","params":{"kind":"command","threadId":"thread-123","turnId":"turn-123","itemId":"item-123","approvalId":"callback-123","command":"git status --short","cwd":"/worktree"}}` + "\n"
	output := &strings.Builder{}
	conn := &appServerConnection{stream: appServerStream{in: output, out: bufio.NewReader(strings.NewReader(requestLine))}}
	_, err := conn.waitTurn(context.Background(), AppServerTurn{ThreadID: "thread-123", TurnID: "turn-123"})
	var required *AppServerApprovalRequiredError
	if !errors.As(err, &required) {
		t.Fatalf("want approval-required error, got %v", err)
	}
	if err := conn.acceptApproval(context.Background(), required.Request); err != nil {
		t.Fatalf("accept approval: %v", err)
	}
	var response struct {
		ID     json.RawMessage `json:"id"`
		Result struct {
			Decision string `json:"decision"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(output.String()), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if string(response.ID) != "42" || response.Result.Decision != "accept" {
		t.Fatalf("response = %s", output.String())
	}
}

func TestBindAppServerApprovalWorktreeUsesDurableScopeAndRejectsSubstitution(t *testing.T) {
	request := AppServerApprovalRequest{RequestID: "request-1", Method: "item/fileChange/requestApproval", ThreadID: "thread-1", TurnID: "turn-1", ItemID: "item-1", Digest: "stale"}
	bound, err := BindAppServerApprovalWorktree(request, "/worktree")
	if err != nil || bound.Worktree != "/worktree" || bound.Digest == "" || bound.Digest == request.Digest {
		t.Fatalf("bound approval = %#v err=%v", bound, err)
	}
	if _, err := BindAppServerApprovalWorktree(bound, "/other-worktree"); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("worktree substitution error = %v, want conflict", err)
	}
}

func TestAppServerPendingFileChangeAcceptsOnlyDeterministicWorktreeBinding(t *testing.T) {
	requestLine := `{"jsonrpc":"2.0","id":"approval-file","method":"item/fileChange/requestApproval","params":{"threadId":"thread-123","turnId":"turn-123","itemId":"item-123"}}` + "\n"
	output := &strings.Builder{}
	conn := &appServerConnection{stream: appServerStream{in: output, out: bufio.NewReader(strings.NewReader(requestLine))}}
	_, err := conn.waitTurn(context.Background(), AppServerTurn{ThreadID: "thread-123", TurnID: "turn-123"})
	var required *AppServerApprovalRequiredError
	if !errors.As(err, &required) {
		t.Fatalf("want approval-required error, got %v", err)
	}
	bound, err := BindAppServerApprovalWorktree(required.Request, "/worktree")
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.acceptApproval(context.Background(), bound); err != nil {
		t.Fatalf("accept deterministic binding: %v", err)
	}
	if !strings.Contains(output.String(), `"decision":"accept"`) {
		t.Fatalf("native response missing accept: %s", output.String())
	}
	forged := bound
	forged.Worktree = "/other-worktree"
	if err := conn.validatePendingApproval(forged); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("forged post-consumption request error = %v, want conflict", err)
	}
}

func TestAppServerApprovalIsExactDigestBoundAndNeverAutoApproved(t *testing.T) {
	request := `{"jsonrpc":"2.0","id":"approval-1","method":"item/commandExecution/requestApproval","params":{"kind":"command","threadId":"thread-123","turnId":"turn-123","itemId":"item-123","approvalId":"callback-123","command":"git status --short","cwd":"/worktree"}}` + "\n"
	output := &strings.Builder{}
	conn := &appServerConnection{stream: appServerStream{in: output, out: bufio.NewReader(strings.NewReader(request))}}
	_, err := conn.waitTurn(context.Background(), AppServerTurn{ThreadID: "thread-123", TurnID: "turn-123"})
	var required *AppServerApprovalRequiredError
	if !errors.As(err, &required) {
		t.Fatalf("want approval-required error, got %v", err)
	}
	if required.Request.Digest == "" || required.Request.Command != "git status --short" || required.Request.Worktree != "/worktree" {
		t.Fatalf("approval lost exact binding: %#v", required.Request)
	}
	if !errors.Is(err, model.ErrPolicyDenied) {
		t.Fatalf("approval must fail closed, got %v", err)
	}
	if err := conn.declineApproval(context.Background(), required.Request); err != nil {
		t.Fatalf("decline exact pending approval: %v", err)
	}
	if !strings.Contains(output.String(), `"decision":"decline"`) || !strings.Contains(output.String(), `"id":"approval-1"`) {
		t.Fatalf("decline response is not exact and typed: %s", output.String())
	}
	if err := conn.declineApproval(context.Background(), required.Request); err == nil {
		t.Fatal("replayed approval response must be rejected")
	}
}

func TestAppServerResolveApprovalRequiresCanonicalAuthorityAndConsumesExactRequest(t *testing.T) {
	requestLine := `{"jsonrpc":"2.0","id":"approval-2","method":"item/commandExecution/requestApproval","params":{"kind":"command","threadId":"thread-123","turnId":"turn-123","itemId":"item-123","approvalId":"callback-123","command":"git status --short","cwd":"/worktree"}}` + "\n"
	output := &strings.Builder{}
	conn := &appServerConnection{stream: appServerStream{in: output, out: bufio.NewReader(strings.NewReader(requestLine))}}
	_, err := conn.waitTurn(context.Background(), AppServerTurn{ThreadID: "thread-123", TurnID: "turn-123"})
	var required *AppServerApprovalRequiredError
	if !errors.As(err, &required) {
		t.Fatalf("want approval-required error, got %v", err)
	}
	server := &AppServer{connection: conn}
	if err := server.ResolveApproval(context.Background(), required.Request, nil); !errors.Is(err, model.ErrPolicyDenied) {
		t.Fatalf("nil authority = %v, want policy denial", err)
	}
	authority := &testAppServerApprovalAuthority{}
	if err := server.ResolveApproval(context.Background(), required.Request, authority); err != nil {
		t.Fatalf("canonical approval: %v", err)
	}
	if authority.seen.Digest != required.Request.Digest || authority.seen.ItemID != required.Request.ItemID {
		t.Fatalf("authority lost exact native binding: got %#v want %#v", authority.seen, required.Request)
	}
	if !strings.Contains(output.String(), `"decision":"accept"`) {
		t.Fatalf("canonical approval did not send typed accept: %s", output.String())
	}
	if err := server.ResolveApproval(context.Background(), required.Request, authority); err == nil {
		t.Fatal("consumed native approval must not be replayable")
	}
}

func TestAppServerResolveApprovalDeclinesWhenAuthorityRejects(t *testing.T) {
	requestLine := `{"jsonrpc":"2.0","id":"approval-3","method":"item/commandExecution/requestApproval","params":{"kind":"command","threadId":"thread-123","turnId":"turn-123","itemId":"item-123","approvalId":"callback-123","command":"git status --short","cwd":"/worktree"}}` + "\n"
	output := &strings.Builder{}
	conn := &appServerConnection{stream: appServerStream{in: output, out: bufio.NewReader(strings.NewReader(requestLine))}}
	_, err := conn.waitTurn(context.Background(), AppServerTurn{ThreadID: "thread-123", TurnID: "turn-123"})
	var required *AppServerApprovalRequiredError
	if !errors.As(err, &required) {
		t.Fatalf("want approval-required error, got %v", err)
	}
	deny := errors.New("canonical approval denied")
	server := &AppServer{connection: conn}
	if err := server.ResolveApproval(context.Background(), required.Request, &testAppServerApprovalAuthority{err: deny}); !errors.Is(err, deny) {
		t.Fatalf("authority rejection = %v, want %v", err, deny)
	}
	if !strings.Contains(output.String(), `"decision":"decline"`) || strings.Contains(output.String(), `"decision":"accept"`) {
		t.Fatalf("rejected authority must send only native decline: %s", output.String())
	}
}

func TestEnsureGovernedCodexHomeMirrorsAuthAndGuaranteesConfigFree(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("CODEX_HOME", "")

	hostCodex := filepath.Join(fakeHome, ".codex")
	if err := os.MkdirAll(hostCodex, 0o700); err != nil {
		t.Fatal(err)
	}
	// Write host auth.json, version.json and config.toml
	authContent := []byte(`{"tokens":{"access_token":"secret-token"}}`)
	if err := os.WriteFile(filepath.Join(hostCodex, "auth.json"), authContent, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hostCodex, "config.toml"), []byte("sandbox_mode = 'danger-full-access'\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	governed, err := EnsureGovernedCodexHome()
	if err != nil {
		t.Fatalf("EnsureGovernedCodexHome failed: %v", err)
	}
	// Verify config.toml is absent in governed directory
	if _, err := os.Stat(filepath.Join(governed, "config.toml")); !os.IsNotExist(err) {
		t.Fatal("governed CODEX_HOME must not contain config.toml")
	}
	// Verify auth.json was mirrored
	mirroredAuth, err := os.ReadFile(filepath.Join(governed, "auth.json"))
	if err != nil || string(mirroredAuth) != string(authContent) {
		t.Fatalf("mirrored auth.json mismatch: err=%v content=%s", err, string(mirroredAuth))
	}
	// Verify RequireAppServerConfigFree succeeds with governed home
	if err := RequireAppServerConfigFree(); err != nil {
		t.Fatalf("RequireAppServerConfigFree must succeed with governed home: %v", err)
	}
}
