package claude

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/model"
)

// fakeStreamAuthority records exactly how the canonical authority was consulted.
type fakeStreamAuthority struct {
	calls     int
	seen      []StreamApprovalRequest
	returnErr error
}

func (f *fakeStreamAuthority) ApproveStreamRequest(_ context.Context, request StreamApprovalRequest) error {
	f.calls++
	f.seen = append(f.seen, request)
	return f.returnErr
}

func governedRequest() adapter.Request {
	return adapter.Request{
		TaskID:   "T-001",
		Title:    "governed task",
		Worktree: "/srv/marshal/worktrees/T-001",
	}
}

func argValue(args []string, flag string) (string, bool) {
	for i, arg := range args {
		if arg == flag && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

func hasArg(args []string, flag string) bool {
	for _, arg := range args {
		if arg == flag {
			return true
		}
	}
	return false
}

func TestStreamTurnArgsRejectsIncompleteRequest(t *testing.T) {
	cases := map[string]adapter.Request{
		"missing task id":  {Title: "t", Worktree: "/srv/wt"},
		"missing title":    {TaskID: "T-001", Worktree: "/srv/wt"},
		"missing worktree": {TaskID: "T-001", Title: "t"},
	}
	for name, request := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := streamTurnArgs(StreamSession{}, request); !errors.Is(err, model.ErrInvalid) {
				t.Fatalf("expected ErrInvalid, got %v", err)
			}
		})
	}
}

func TestStreamTurnArgsRejectsUncleanWorktree(t *testing.T) {
	for _, worktree := range []string{
		"relative/worktree",
		"./worktree",
		"/srv/marshal/../etc",
		"/srv/marshal/worktrees/",
		"/srv//marshal",
	} {
		t.Run(worktree, func(t *testing.T) {
			request := governedRequest()
			request.Worktree = worktree
			if _, err := streamTurnArgs(StreamSession{}, request); !errors.Is(err, model.ErrInvalid) {
				t.Fatalf("expected ErrInvalid for %q, got %v", worktree, err)
			}
		})
	}
}

func TestStreamTurnArgsAlwaysReinjectsBoundaries(t *testing.T) {
	request := governedRequest()
	// A resumed session must not be able to inherit weaker boundaries.
	session := StreamSession{SessionID: "sess-abc"}

	args, err := streamTurnArgs(session, request)
	if err != nil {
		t.Fatalf("streamTurnArgs: %v", err)
	}
	if value, ok := argValue(args, "--permission-mode"); !ok || value != "manual" {
		t.Fatalf("expected --permission-mode manual, got %q (present=%v)", value, ok)
	}
	if value, ok := argValue(args, "--permission-prompts"); !ok || value != "none" {
		t.Fatalf("expected --permission-prompts none, got %q (present=%v)", value, ok)
	}
	if !hasArg(args, "--strict-mcp-config") {
		t.Fatalf("expected --strict-mcp-config in %v", args)
	}
	if value, ok := argValue(args, "--add-dir"); !ok || value != request.Worktree {
		t.Fatalf("expected --add-dir %q, got %q (present=%v)", request.Worktree, value, ok)
	}
	if err := ValidateDangerousFlags(args); err != nil {
		t.Fatalf("governed args must never carry a bypass flag: %v", err)
	}
}

func TestStreamTurnArgsResumeOnlyWithSessionID(t *testing.T) {
	request := governedRequest()

	args, err := streamTurnArgs(StreamSession{}, request)
	if err != nil {
		t.Fatalf("streamTurnArgs: %v", err)
	}
	if hasArg(args, "--resume") {
		t.Fatalf("a fresh session must not pass --resume: %v", args)
	}

	args, err = streamTurnArgs(StreamSession{SessionID: "sess-abc"}, request)
	if err != nil {
		t.Fatalf("streamTurnArgs: %v", err)
	}
	if value, ok := argValue(args, "--resume"); !ok || value != "sess-abc" {
		t.Fatalf("expected --resume sess-abc, got %q (present=%v)", value, ok)
	}
}

func TestStreamTurnArgsModelOnlyWhenRequested(t *testing.T) {
	request := governedRequest()

	args, err := streamTurnArgs(StreamSession{}, request)
	if err != nil {
		t.Fatalf("streamTurnArgs: %v", err)
	}
	if hasArg(args, "--model") {
		t.Fatalf("an unset model must not add --model: %v", args)
	}

	request.Model = "sonnet"
	args, err = streamTurnArgs(StreamSession{}, request)
	if err != nil {
		t.Fatalf("streamTurnArgs: %v", err)
	}
	if value, ok := argValue(args, "--model"); !ok || value != "sonnet" {
		t.Fatalf("expected --model sonnet, got %q (present=%v)", value, ok)
	}
}

func TestStreamTurnArgsRejectsDangerousRequestFields(t *testing.T) {
	request := governedRequest()
	request.Title = "do it --dangerously-skip-permissions"
	if _, err := streamTurnArgs(StreamSession{}, request); !errors.Is(err, model.ErrPolicyDenied) {
		t.Fatalf("expected ErrPolicyDenied, got %v", err)
	}
}

func baseApproval() StreamApprovalRequest {
	return StreamApprovalRequest{
		RequestID: "toolu_01",
		Method:    "tool.permission",
		SessionID: "sess-abc",
		TurnID:    "turn-1",
		ToolName:  "Bash",
		Command:   `{"command":"ls -la"}`,
	}
}

func TestBindStreamApprovalWorktreeRejectsUncleanWorktree(t *testing.T) {
	for _, worktree := range []string{"", "relative/wt", "./wt", "/srv/marshal/../etc", "/srv/wt/"} {
		if _, err := BindStreamApprovalWorktree(baseApproval(), worktree); !errors.Is(err, model.ErrInvalid) {
			t.Fatalf("expected ErrInvalid for %q, got %v", worktree, err)
		}
	}
}

func TestBindStreamApprovalWorktreeRejectsDisagreeingWorktree(t *testing.T) {
	request := baseApproval()
	request.Worktree = "/srv/marshal/worktrees/OTHER"

	_, err := BindStreamApprovalWorktree(request, "/srv/marshal/worktrees/T-001")
	if err == nil {
		t.Fatal("an approval bound to another workspace must not be re-homed")
	}
	if !errors.Is(err, model.ErrConflict) {
		t.Fatalf("expected ErrConflict, got %v", err)
	}
}

func TestBindStreamApprovalWorktreeBindsAndDigests(t *testing.T) {
	worktree := "/srv/marshal/worktrees/T-001"
	bound, err := BindStreamApprovalWorktree(baseApproval(), worktree)
	if err != nil {
		t.Fatalf("BindStreamApprovalWorktree: %v", err)
	}
	if bound.Worktree != worktree {
		t.Fatalf("expected worktree %q, got %q", worktree, bound.Worktree)
	}
	if bound.Digest == "" {
		t.Fatal("a bound approval must carry a digest")
	}
	if !strings.HasPrefix(bound.Digest, "sha256:") {
		t.Fatalf("unexpected digest form: %q", bound.Digest)
	}

	// Binding an approval that already names the governed worktree is allowed
	// and must be stable.
	again, err := BindStreamApprovalWorktree(bound, worktree)
	if err != nil {
		t.Fatalf("rebinding the same worktree: %v", err)
	}
	if again.Digest != bound.Digest {
		t.Fatalf("digest must be stable for identical fields: %q vs %q", again.Digest, bound.Digest)
	}
}

func TestStreamApprovalDigestBindsEveryIdentifyingField(t *testing.T) {
	base := baseApproval()
	base.Worktree = "/srv/marshal/worktrees/T-001"
	baseDigest, err := streamApprovalDigest(base)
	if err != nil {
		t.Fatalf("streamApprovalDigest: %v", err)
	}

	mutations := map[string]func(*StreamApprovalRequest){
		"RequestID": func(r *StreamApprovalRequest) { r.RequestID = "toolu_02" },
		"Method":    func(r *StreamApprovalRequest) { r.Method = "tool.permission.v2" },
		"SessionID": func(r *StreamApprovalRequest) { r.SessionID = "sess-xyz" },
		"TurnID":    func(r *StreamApprovalRequest) { r.TurnID = "turn-2" },
		"ToolName":  func(r *StreamApprovalRequest) { r.ToolName = "Write" },
		"Command":   func(r *StreamApprovalRequest) { r.Command = `{"command":"rm -rf /"}` },
		"Worktree":  func(r *StreamApprovalRequest) { r.Worktree = "/srv/marshal/worktrees/T-002" },
	}
	seen := map[string]string{baseDigest: "base"}
	for field, mutate := range mutations {
		altered := base
		mutate(&altered)
		digest, digestErr := streamApprovalDigest(altered)
		if digestErr != nil {
			t.Fatalf("streamApprovalDigest(%s): %v", field, digestErr)
		}
		if digest == baseDigest {
			t.Fatalf("changing %s must change the digest; an approval could be replayed against a different tool call", field)
		}
		if prior, clash := seen[digest]; clash {
			t.Fatalf("digest collision between %s and %s", field, prior)
		}
		seen[digest] = field
	}
}

func TestResolveApprovalRequiresAuthority(t *testing.T) {
	bound, err := BindStreamApprovalWorktree(baseApproval(), "/srv/marshal/worktrees/T-001")
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	client := NewStreamClient("claude", "test")
	if err := client.ResolveApproval(context.Background(), bound, nil); !errors.Is(err, model.ErrPolicyDenied) {
		t.Fatalf("expected ErrPolicyDenied without an authority, got %v", err)
	}
}

func TestResolveApprovalRequiresDigest(t *testing.T) {
	client := NewStreamClient("claude", "test")
	authority := &fakeStreamAuthority{}
	request := baseApproval()
	request.Worktree = "/srv/marshal/worktrees/T-001"

	if err := client.ResolveApproval(context.Background(), request, authority); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("expected ErrInvalid for an unbound request, got %v", err)
	}
	if authority.calls != 0 {
		t.Fatalf("the authority must not be consulted for an unbound request, called %d times", authority.calls)
	}
}

func TestResolveApprovalRejectsTamperedRequest(t *testing.T) {
	bound, err := BindStreamApprovalWorktree(baseApproval(), "/srv/marshal/worktrees/T-001")
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	// The digest was computed over `ls -la`; swapping the command afterwards is
	// exactly the replay this binding exists to stop.
	tampered := bound
	tampered.Command = `{"command":"rm -rf /"}`

	client := NewStreamClient("claude", "test")
	authority := &fakeStreamAuthority{}
	err = client.ResolveApproval(context.Background(), tampered, authority)
	if err == nil {
		t.Fatal("a tampered request must not be resolved")
	}
	if !errors.Is(err, model.ErrConflict) {
		t.Fatalf("expected ErrConflict, got %v", err)
	}
	if authority.calls != 0 {
		t.Fatalf("the authority must not be consulted for a tampered request, called %d times", authority.calls)
	}
}

func TestResolveApprovalConsultsAuthorityExactlyOnce(t *testing.T) {
	bound, err := BindStreamApprovalWorktree(baseApproval(), "/srv/marshal/worktrees/T-001")
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	client := NewStreamClient("claude", "test")
	authority := &fakeStreamAuthority{}

	if err := client.ResolveApproval(context.Background(), bound, authority); err != nil {
		t.Fatalf("ResolveApproval: %v", err)
	}
	if authority.calls != 1 {
		t.Fatalf("expected exactly one authority call, got %d", authority.calls)
	}
	if len(authority.seen) != 1 || authority.seen[0] != bound {
		t.Fatalf("the authority must see the exact bound request, got %+v", authority.seen)
	}
}

func TestResolveApprovalSurfacesAuthorityRefusal(t *testing.T) {
	bound, err := BindStreamApprovalWorktree(baseApproval(), "/srv/marshal/worktrees/T-001")
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	client := NewStreamClient("claude", "test")
	authority := &fakeStreamAuthority{returnErr: model.ErrPolicyDenied}

	if err := client.ResolveApproval(context.Background(), bound, authority); !errors.Is(err, model.ErrPolicyDenied) {
		t.Fatalf("expected the authority's refusal to propagate, got %v", err)
	}
}

func TestDeclineApprovalRequiresDigest(t *testing.T) {
	client := NewStreamClient("claude", "test")
	if err := client.DeclineApproval(context.Background(), baseApproval()); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("expected ErrInvalid for an unbound request, got %v", err)
	}
	bound, err := BindStreamApprovalWorktree(baseApproval(), "/srv/marshal/worktrees/T-001")
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	if err := client.DeclineApproval(context.Background(), bound); err != nil {
		t.Fatalf("declining a bound request must close the turn cleanly, got %v", err)
	}
}

func int64Ptr(value int64) *int64 { return &value }

func TestStreamUsageKeepsUnknownUnknown(t *testing.T) {
	usage := streamUsage(streamEvent{Type: "result", Subtype: "success", NumTurns: 3, DurationMs: 1200})
	if usage.Reported {
		t.Fatal("a result with no usage block must not be reported as measured")
	}
	if usage.PromptTokens != nil || usage.CompletionTokens != nil || usage.TotalTokens != nil {
		t.Fatalf("missing token counts must stay nil, got %+v", usage)
	}
	if usage.CostUSD != nil {
		t.Fatalf("missing cost must stay nil, got %v", *usage.CostUSD)
	}
	if usage.ModelCalls != 3 || usage.DurationMs != 1200 {
		t.Fatalf("locally observed fields must still be carried, got %+v", usage)
	}
}

func TestStreamUsageCountsCacheTokensAsPromptSide(t *testing.T) {
	cost := 0.0421
	event := streamEvent{
		Type: "result", Subtype: "success", NumTurns: 2, DurationMs: 5000,
		TotalCostUSD: &cost,
		Usage: &streamUsageRaw{
			InputTokens:              int64Ptr(100),
			OutputTokens:             int64Ptr(50),
			CacheReadInputTokens:     int64Ptr(1000),
			CacheCreationInputTokens: int64Ptr(400),
		},
	}
	usage := streamUsage(event)
	if !usage.Reported {
		t.Fatal("a usage block must mark the turn as reported")
	}
	if usage.PromptTokens == nil || *usage.PromptTokens != 1500 {
		t.Fatalf("prompt tokens must be input+cache_read+cache_creation = 1500, got %v", usage.PromptTokens)
	}
	if usage.CompletionTokens == nil || *usage.CompletionTokens != 50 {
		t.Fatalf("expected 50 completion tokens, got %v", usage.CompletionTokens)
	}
	if usage.TotalTokens == nil || *usage.TotalTokens != 1550 {
		t.Fatalf("expected 1550 total tokens, got %v", usage.TotalTokens)
	}
	if usage.CostUSD == nil || *usage.CostUSD != cost {
		t.Fatalf("expected cost %v, got %v", cost, usage.CostUSD)
	}
}

func TestStreamUsagePartialFieldsStayPartial(t *testing.T) {
	// Output tokens absent: a total must not be invented from half the data.
	usage := streamUsage(streamEvent{Usage: &streamUsageRaw{InputTokens: int64Ptr(10)}})
	if usage.PromptTokens == nil || *usage.PromptTokens != 10 {
		t.Fatalf("expected 10 prompt tokens, got %v", usage.PromptTokens)
	}
	if usage.CompletionTokens != nil {
		t.Fatalf("absent output tokens must stay nil, got %d", *usage.CompletionTokens)
	}
	if usage.TotalTokens != nil {
		t.Fatalf("a total must not be synthesized from partial data, got %d", *usage.TotalTokens)
	}
}

func TestStreamApprovalFromEventIgnoresEventsWithoutDenial(t *testing.T) {
	turn := StreamTurn{SessionID: "sess-abc", TurnID: "turn-1"}
	if _, ok := streamApprovalFromEvent(streamEvent{Type: "result"}, turn, "/srv/wt"); ok {
		t.Fatal("a result with no permission denial must not become an approval pause")
	}
	event := streamEvent{Type: "result"}
	event.PermissionDenies = append(event.PermissionDenies, struct {
		ToolName  string          `json:"tool_name"`
		ToolUseID string          `json:"tool_use_id"`
		ToolInput json.RawMessage `json:"tool_input"`
	}{ToolName: "   "})
	if _, ok := streamApprovalFromEvent(event, turn, "/srv/wt"); ok {
		t.Fatal("a denial with no tool identity must not become an approval pause")
	}
}

func TestStreamApprovalFromEventCarriesExactIdentity(t *testing.T) {
	turn := StreamTurn{SessionID: "sess-abc", TurnID: "turn-1"}
	event := streamEvent{Type: "result", UUID: "event-uuid"}
	event.PermissionDenies = append(event.PermissionDenies, struct {
		ToolName  string          `json:"tool_name"`
		ToolUseID string          `json:"tool_use_id"`
		ToolInput json.RawMessage `json:"tool_input"`
	}{ToolName: "Bash", ToolUseID: "toolu_01", ToolInput: json.RawMessage(`{"command":"ls"}`)})

	approval, ok := streamApprovalFromEvent(event, turn, "/srv/marshal/worktrees/T-001")
	if !ok {
		t.Fatal("expected an approval request")
	}
	if approval.RequestID != "toolu_01" || approval.ToolName != "Bash" {
		t.Fatalf("unexpected identity: %+v", approval)
	}
	if approval.SessionID != turn.SessionID || approval.TurnID != turn.TurnID {
		t.Fatalf("approval must be bound to the live turn: %+v", approval)
	}
	if approval.Method != "tool.permission" {
		t.Fatalf("unexpected method: %q", approval.Method)
	}
	if approval.Worktree != "/srv/marshal/worktrees/T-001" {
		t.Fatalf("unexpected worktree: %q", approval.Worktree)
	}
	if approval.Digest != "" {
		t.Fatal("an unbound observation must not claim a digest")
	}
}

func TestStreamApprovalFromEventFallsBackToEventUUID(t *testing.T) {
	turn := StreamTurn{SessionID: "sess-abc", TurnID: "turn-1"}
	event := streamEvent{Type: "result", UUID: "event-uuid"}
	event.PermissionDenies = append(event.PermissionDenies, struct {
		ToolName  string          `json:"tool_name"`
		ToolUseID string          `json:"tool_use_id"`
		ToolInput json.RawMessage `json:"tool_input"`
	}{ToolName: "Bash"})

	approval, ok := streamApprovalFromEvent(event, turn, "/srv/wt")
	if !ok {
		t.Fatal("expected an approval request")
	}
	if approval.RequestID != "event-uuid" {
		t.Fatalf("expected the event UUID as a fallback request id, got %q", approval.RequestID)
	}
}

func TestStreamApprovalFromEventBoundsProviderText(t *testing.T) {
	turn := StreamTurn{SessionID: "sess-abc", TurnID: "turn-1"}
	huge := json.RawMessage(`"` + strings.Repeat("A", 8192) + `"`)
	event := streamEvent{Type: "result"}
	event.PermissionDenies = append(event.PermissionDenies, struct {
		ToolName  string          `json:"tool_name"`
		ToolUseID string          `json:"tool_use_id"`
		ToolInput json.RawMessage `json:"tool_input"`
	}{ToolName: "Bash", ToolUseID: "toolu_01", ToolInput: huge})

	approval, ok := streamApprovalFromEvent(event, turn, "/srv/wt")
	if !ok {
		t.Fatal("expected an approval request")
	}
	if len([]rune(approval.Command)) > 2049 {
		t.Fatalf("provider-authored tool input must be bounded, got %d runes", len([]rune(approval.Command)))
	}
}

func TestBoundedStreamBufferStopsAtLimit(t *testing.T) {
	buffer := &boundedStreamBuffer{limit: 16}
	n, err := buffer.Write([]byte("0123456789"))
	if err != nil || n != 10 {
		t.Fatalf("Write: n=%d err=%v", n, err)
	}
	// A short write would make the process see a broken pipe, so the writer
	// must always claim the full length while discarding the overflow.
	n, err = buffer.Write([]byte(strings.Repeat("x", 1000)))
	if err != nil || n != 1000 {
		t.Fatalf("Write: n=%d err=%v", n, err)
	}
	if got := len(buffer.Bytes()); got != 16 {
		t.Fatalf("expected the buffer to stop at its 16-byte limit, got %d", got)
	}
	if !strings.HasPrefix(string(buffer.Bytes()), "0123456789") {
		t.Fatalf("the head must be preserved, got %q", buffer.Bytes())
	}
}

func TestBoundedStreamBufferReturnsCopy(t *testing.T) {
	buffer := &boundedStreamBuffer{limit: 64}
	if _, err := buffer.Write([]byte("startup failure")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	snapshot := buffer.Bytes()
	snapshot[0] = 'X'
	if strings.HasPrefix(string(buffer.Bytes()), "X") {
		t.Fatal("Bytes must return a copy, not the live buffer")
	}
}

func TestNewStreamClientRefusesEmptyBinary(t *testing.T) {
	if NewStreamClient("", "test") != nil {
		t.Fatal("a stream client without a binary must not be constructed")
	}
}

func TestStartSessionValidatesRequestBeforeBinding(t *testing.T) {
	client := NewStreamClient("claude", "test")
	bad := governedRequest()
	bad.Worktree = "relative/wt"
	if _, err := client.StartSession(context.Background(), bad); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("expected ErrInvalid, got %v", err)
	}

	session, err := client.StartSession(context.Background(), governedRequest())
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if session.SessionID != "" {
		t.Fatalf("no native session exists until a turn runs, got %q", session.SessionID)
	}
	if session.Protocol != streamProtocolVersion {
		t.Fatalf("unexpected protocol: %q", session.Protocol)
	}
}

func TestResumeSessionRejectsNonNativeSessionID(t *testing.T) {
	client := NewStreamClient("claude", "test")
	for _, id := range []string{"", "   ", "sess abc", "sess;rm -rf /"} {
		if _, err := client.ResumeSession(context.Background(), id, governedRequest()); !errors.Is(err, model.ErrInvalid) {
			t.Fatalf("expected ErrInvalid for %q, got %v", id, err)
		}
	}
	session, err := client.ResumeSession(context.Background(), "sess-abc", governedRequest())
	if err != nil {
		t.Fatalf("ResumeSession: %v", err)
	}
	if session.SessionID != "sess-abc" {
		t.Fatalf("expected the native session id to be bound, got %q", session.SessionID)
	}
}
