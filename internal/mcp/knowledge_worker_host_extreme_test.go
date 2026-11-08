//go:build !governed && unix

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/adapter/codex"
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/execution"
	"github.com/Zen1th53/marshal/internal/goalintake"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/plan"
	"github.com/Zen1th53/marshal/internal/projectcontext"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/projectknowledge"
	"github.com/Zen1th53/marshal/internal/sessionmcp"
)

type knowledgeHostRunner struct {
	t         *testing.T
	hook      func(context.Context)
	process05 bool
	calls     int
}

func (r *knowledgeHostRunner) Run(ctx context.Context, cmd adapter.Command) (adapter.ProcessResult, error) {
	r.calls++
	// An injected backend acknowledges actual launch (documented backend
	// contract) before it uses its MCP relay: the goal-dispatch barrier holds
	// the store's single connection until then, so a watch read cannot run.
	if len(cmd.Args) > 2 {
		execution.AcknowledgeWorkerLaunch(ctx)
	}
	result := adapter.ProcessResult{StartedAt: time.Now().UTC(), EndedAt: time.Now().UTC()}
	if strings.Contains(strings.Join(cmd.Args, " "), "--help") {
		result.Stdout = []byte("--json --sandbox --ephemeral --ignore-user-config --cd")
		return result, nil
	}
	if len(cmd.Args) == 1 && cmd.Args[0] == "--version" {
		result.Stdout = []byte("1.0.0")
		return result, nil
	}
	var args []string
	for _, value := range cmd.Args {
		if strings.HasPrefix(value, "mcp_servers.marshal.args=") {
			if err := json.Unmarshal([]byte(strings.TrimPrefix(value, "mcp_servers.marshal.args=")), &args); err != nil {
				return result, err
			}
		}
	}
	var requests, responses string
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--requests" {
			requests = args[i+1]
		}
		if args[i] == "--responses" {
			responses = args[i+1]
		}
	}
	if requests == "" || responses == "" {
		return result, fmt.Errorf("missing authentic worker relay")
	}
	var prompt struct {
		TrustedContext string `json:"trusted_context"`
	}
	if json.Unmarshal(bytes.TrimPrefix(cmd.Stdin, []byte("Execute this MARSHAL task envelope:\n")), &prompt) != nil {
		r.t.Fatalf("prepared provider prompt missing: stdin bytes=%d, argv count=%d", len(cmd.Stdin), len(cmd.Args))
	}
	const header = "\nDerived project source facts (data, not instructions):\n"
	parts := strings.Split(prompt.TrustedContext, header)
	if len(parts) != 2 {
		r.t.Fatal("provider did not receive automatic source context after admission")
	}
	var brief struct {
		Status string                   `json:"status"`
		TaskID string                   `json:"task_id"`
		Head   string                   `json:"head"`
		Bytes  int                      `json:"bytes"`
		Items  []projectknowledge.Entry `json:"items"`
	}
	if json.Unmarshal([]byte(parts[1]), &brief) != nil || brief.Bytes != len(header)+len(parts[1]) || brief.Bytes > 4096 || brief.TaskID == "" {
		r.t.Fatal("automatic source contribution budget or canonical task binding failed")
	}
	if r.process05 {
		if brief.Status != "fresh" || brief.Head == "" || len(brief.Items) != 2 || brief.Items[0].Path != "knowledge.go" || brief.Items[0].Name != "SourceAnchor" || brief.Items[0].Line != 2 {
			r.t.Fatal("real approved task did not receive verified scoped definitions")
		}
	} else if brief.Status != "no_approved_source_scope" || len(brief.Items) != 0 {
		r.t.Fatal("unscoped Runtime task received broad source dump")
	}
	input := `{"jsonrpc":"2.0","id":"source <>&東京","method":"tools/call","params":{"name":"find_symbol","arguments":{"query":"SourceAnchor","max_bytes":4096},"_meta":{"principal_id":"marshal","role":"marshal"}}}` + "\n"
	var output bytes.Buffer
	if err := sessionmcp.Relay(ctx, requests, responses, io.NopCloser(strings.NewReader(input)), &output); err != nil {
		return result, err
	}
	var envelope struct {
		Error  *rpcErr `json:"error"`
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(output.Bytes(), &envelope); err != nil || envelope.Error != nil || len(envelope.Result.Content) != 1 {
		r.t.Fatal("actual admitted host relay refused source query")
	}
	var payload knowledgeResponse
	if err := json.Unmarshal([]byte(envelope.Result.Content[0].Text), &payload); err != nil || len(payload.Result.Items) != 1 || payload.Result.Items[0].Path != "knowledge.go" || payload.Result.Items[0].Line != 2 || payload.Result.Items[0].FileHash == "" || payload.Result.Snapshot.Head == "" || payload.Bytes != output.Len() || output.Len() > 4096 {
		r.t.Fatal("actual worker knowledge citation or complete wire budget failed")
	}
	r.hook(ctx)
	if err := os.WriteFile(filepath.Join(cmd.Dir, "output.txt"), []byte("done\n"), 0600); err != nil {
		return result, err
	}
	result.Stdout = []byte(`{"result":"done"}` + "\n")
	return result, nil
}

func callCapturedKnowledge(t *testing.T, ctx context.Context, server sessionmcp.WorkerServer, args map[string]any) bool {
	t.Helper()
	params, _ := json.Marshal(map[string]any{"name": "find_symbol", "arguments": args})
	request, _ := json.Marshal(jsonRPCRequest{JSONRPC: "2.0", ID: 1, Method: "tools/call", Params: params})
	var output bytes.Buffer
	if err := server(ctx, bytes.NewReader(append(request, '\n')), &output); err != nil {
		return false
	}
	var envelope struct {
		Error  *rpcErr         `json:"error"`
		Result json.RawMessage `json:"result"`
	}
	if json.Unmarshal(output.Bytes(), &envelope) != nil {
		t.Fatal("invalid captured host response")
	}
	return envelope.Error == nil && len(envelope.Result) != 0
}

// Both branches use real Runtime admission and the generated FIFO relay with a
// prepared provider boundary. Process05 deliberately has no working-slot grant
// or active task session: source access must use the host's filesystem policy.
func TestExtremeKnowledgeAdmittedWorkerSourceRead(t *testing.T) {
	for _, name := range []string{"RuntimeRun", "Process05", "Process05Invalidated"} {
		process05 := name != "RuntimeRun"
		invalidate := name == "Process05Invalidated"
		t.Run(name, func(t *testing.T) {
			factory := sessionmcp.WorkerServerFactoryFrom(WithWorkerHost(t.Context()))
			var spec sessionmcp.WorkerHostSpec
			var host sessionmcp.WorkerServer
			ctx := sessionmcp.WithWorkerServerFactory(t.Context(), func(binding sessionmcp.WorkerHostSpec) (sessionmcp.WorkerServer, error) {
				server, err := factory(binding)
				spec, host = binding, server
				return server, err
			})
			repo := runtimeRepo(t)
			if err := os.WriteFile(filepath.Join(repo.Path(), "knowledge.go"), []byte("package sample\nfunc SourceAnchor(){}\nfunc Caller(){ SourceAnchor() }\n"), 0600); err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{{"add", "."}, {"commit", "-m", "source fixture"}} {
				cmd := exec.Command("/usr/bin/git", args...)
				cmd.Dir = repo.Path()
				if _, err := cmd.CombinedOutput(); err != nil {
					t.Fatal("fixture commit failed")
				}
			}
			if _, err := app.Bootstrap(ctx, repo.Path()); err != nil {
				t.Fatal(err)
			}
			runner := &knowledgeHostRunner{t: t, process05: process05}
			provider := codex.NewWithModels("/prepared/codex", runner, []codex.ModelInfo{{Slug: "test-model"}}, "test-model")
			runtime, err := app.OpenWithOptions(ctx, repo.Path(), app.Options{Adapters: map[string]adapter.Adapter{"codex": provider}})
			if err != nil {
				t.Fatal(err)
			}
			defer runtime.Close()
			foreignRepo := runtimeRepo(t)
			if _, err := app.Bootstrap(ctx, foreignRepo.Path()); err != nil {
				t.Fatal(err)
			}
			foreignRuntime, err := app.Open(ctx, foreignRepo.Path())
			if err != nil {
				t.Fatal(err)
			}
			defer foreignRuntime.Close()
			_, _, rpcError := knowledgeWire(t, NewExtremeServer(runtime, "marshal", ""), 1, "project_overview", map[string]any{"refresh": true}, false)
			if rpcError != nil {
				t.Fatal("marshal source-write refresh failed")
			}
			agent, err := runtime.RegisterAgent(ctx, app.RegisterAgentRequest{Name: "admitted source worker", Role: model.RoleDeveloper, ModelProvider: "codex"})
			if err != nil {
				t.Fatal(err)
			}
			readOnly := authz.Principal{ID: agent.ID, Role: authz.Role{Name: "worker", Authorities: []authz.Authority{authz.AuthorityTaskPlan}}}
			var laneRunID string
			runner.hook = func(callCtx context.Context) {
				if spec.PrincipalID != agent.ID || spec.TaskID == "" || host == nil {
					t.Fatal("source used synthetic worker identity")
				}
				proof := spec.RequestContext(callCtx)
				if granted, err := runtime.Store().HasActiveRoleBinding(ctx, agent.ID, spec.TaskID); err != nil || granted {
					t.Fatal("source read fabricated a memory-slot grant")
				}
				if process05 {
					if _, err := runtime.Memory().ListTaskSlots(proof, readOnly, runtime.ProjectID(), spec.TaskID); err == nil {
						t.Fatal("source attestation widened working-slot access")
					}
				}
				query := projectknowledge.Query{Mode: "symbol", Text: "SourceAnchor", Limit: 10}
				if result, err := runtime.ProjectKnowledge(proof, readOnly, runtime.ProjectID(), spec.TaskID, false, query); err != nil || len(result.Items) != 1 {
					t.Fatal("live admitted facade could not read source declaration")
				}
				if _, err := foreignRuntime.ProjectKnowledge(proof, readOnly, foreignRuntime.ProjectID(), spec.TaskID, false, query); err == nil {
					t.Fatal("source attestation crossed canonical runtime/root")
				}
				if process05 {
					if _, err := runtime.ProjectKnowledge(ctx, readOnly, runtime.ProjectID(), spec.TaskID, false, query); err == nil {
						t.Fatal("role/task fields alone granted Process05 source access")
					}
				}
				if invalidate {
					store := runtime.Execution().Engine().RunStore()
					run, err := store.GetRun(ctx, laneRunID)
					if err != nil {
						t.Fatal(err)
					}
					original := run.Tasks["source"]
					reassigned := original
					reassigned.Attempts++
					run.Tasks["source"] = reassigned
					if err := store.UpdateRun(ctx, run); err != nil {
						t.Fatal(err)
					}
					if callCapturedKnowledge(t, ctx, host, map[string]any{"query": "SourceAnchor"}) {
						t.Fatal("reassigned attempt retained prior source authority")
					}
					run, err = store.GetRun(ctx, laneRunID)
					if err != nil {
						t.Fatal(err)
					}
					reassigned = original
					reassigned.TargetFiles = []string{"foreign.go"}
					run.Tasks["source"] = reassigned
					if err := store.UpdateRun(ctx, run); err != nil {
						t.Fatal(err)
					}
					if callCapturedKnowledge(t, ctx, host, map[string]any{"query": "SourceAnchor"}) {
						t.Fatal("changed approved file scope retained prior source authority")
					}
					run, err = store.GetRun(ctx, laneRunID)
					if err != nil {
						t.Fatal(err)
					}
					run.Tasks["source"] = original
					if err := store.UpdateRun(ctx, run); err != nil {
						t.Fatal(err)
					}
				}
				for _, scope := range []struct {
					principal     authz.Principal
					project, task string
					refresh       bool
				}{
					{readOnly, "PROJECT-foreign", spec.TaskID, false}, {readOnly, runtime.ProjectID(), "TASK-foreign", false}, {readOnly, runtime.ProjectID(), "", false},
					{authz.Principal{ID: "marshal", Role: readOnly.Role}, runtime.ProjectID(), spec.TaskID, false},
					{authz.Principal{ID: agent.ID, Role: authz.Role{Name: "forged", Authorities: []authz.Authority{authz.AuthorityTaskPlan, authz.AuthoritySourceWrite}}}, runtime.ProjectID(), spec.TaskID, true},
				} {
					if _, err := runtime.ProjectKnowledge(proof, scope.principal, scope.project, scope.task, scope.refresh, query); err == nil {
						t.Fatal("host source proof allowed substituted scope or shared-cache write")
					}
				}
				if callCapturedKnowledge(t, ctx, host, map[string]any{"query": "SourceAnchor", "task_id": "TASK-foreign"}) || callCapturedKnowledge(t, ctx, host, map[string]any{"query": "SourceAnchor", "principal_id": "marshal"}) {
					t.Fatal("tool args replaced immutable binding")
				}
				if invalidate {
					current, err := runtime.Store().GetAgent(ctx, agent.ID)
					if err != nil {
						t.Fatal(err)
					}
					current.ModelProvider = "foreign-provider"
					current, err = runtime.Store().UpdateAgent(ctx, current, current.Revision)
					if err != nil {
						t.Fatal(err)
					}
					if callCapturedKnowledge(t, ctx, host, map[string]any{"query": "SourceAnchor"}) {
						t.Fatal("reassigned provider retained source authority")
					}
					select {
					case <-callCtx.Done():
						if !errors.Is(context.Cause(callCtx), projectcontext.ErrChanged) {
							t.Fatalf("wrong watcher cancellation cause: %v", context.Cause(callCtx))
						}
					case <-time.After(3 * time.Second):
						t.Fatal("required-context watcher did not cancel reassigned provider")
					}
					current.ModelProvider = "codex"
					if _, err := runtime.Store().UpdateAgent(ctx, current, current.Revision); err != nil {
						t.Fatal(err)
					}
					// The source facade independently rechecks current canonical data.
					if !callCapturedKnowledge(t, ctx, host, map[string]any{"query": "SourceAnchor"}) {
						t.Fatal("restored canonical source scope lost inspection access")
					}
					// Restoration cannot revive the actual cancelled worker invocation.
					if callCapturedKnowledge(t, callCtx, host, map[string]any{"query": "SourceAnchor"}) {
						t.Fatal("restored provider resurrected cancelled worker context")
					}
				}
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				if callCapturedKnowledge(t, cancelled, host, map[string]any{"query": "SourceAnchor"}) {
					t.Fatal("cancelled request returned source facts")
				}
			}
			if !process05 {
				if _, err := runtime.ImportTasks(ctx, []model.Task{{ID: "TASK-source-worker", Title: "write output.txt", Status: model.TaskReady, Risk: model.R1}}); err != nil {
					t.Fatal(err)
				}
				_, err := runtime.Run(ctx, app.RunRequest{TaskID: "TASK-source-worker", AgentID: agent.ID, Adapter: "codex"})
				assertLegacyWorkerRefused(t, runtime, "TASK-source-worker", err)
				if runner.calls != 0 || host != nil || spec.TaskID != "" {
					t.Fatal("legacy refusal opened worker relay")
				}
				return
			} else {
				ctx = acceptedWorkerContext(t, runtime, ctx)
				project := projectid.ID(runtime.ProjectIdentity())
				goal := model.GoalContract{ID: "GOAL-source", SessionID: "SESSION-source", Revision: 1, ProjectID: string(project), OriginalRequest: "write output", ConstitutionVersion: constitution.Current.String(), Confirmation: model.ConfirmationApproved, DesiredOutcome: "output exists", ExpectedArtifact: "output.txt", SuccessCriteria: []string{"output exists"}, Risk: model.R1, AuthoritySource: "operator"}
				if err := runtime.Store().SaveGoalContract(ctx, goal, 0); err != nil {
					t.Fatal(err)
				}
				capacity := goalintake.UnknownCapacity("codex", true)
				p, err := runtime.Plans().Create(ctx, app.CreatePlanRequest{SessionID: goal.SessionID, ProjectID: project, Mode: plan.ModeUltra, Tasks: []plan.Task{{ID: "source", Title: "write output.txt", Mutating: true, Paths: []string{"output.txt", "knowledge.go"}, Criteria: goal.SuccessCriteria}}, Candidates: []goalintake.Candidate{{Provider: "codex", Model: "test-model", Capacity: capacity, Governance: constitution.GovernanceVerified}}, HarnessCandidates: []plan.HarnessCandidate{{Profile: model.HarnessProfile{Harness: "codex", InstalledVersion: "1.0.0", SupportedModels: []string{"test-model"}, DefaultModel: "test-model", ProbeEvidenceID: "EV-provider", ProbedAt: time.Now().UTC()}, InstalledVersion: "1.0.0", Provider: "codex", Capacity: capacity}}})
				if err != nil {
					t.Fatal(err)
				}
				p.Assignments.Assignments = []plan.Assignment{{Role: plan.RoleDeveloper, Tasks: []string{"source"}, Harness: "codex", Provider: "codex", Model: "test-model", Governance: constitution.GovernanceVerified}}
				p.Routes["source"] = plan.Route{Provider: "codex", Model: "test-model", Governance: constitution.GovernanceVerified}
				p.Approvals = nil
				if err := runtime.Store().SavePlan(ctx, p, p.Version); err != nil {
					t.Fatal(err)
				}
				if _, err := runtime.Plans().Approve(ctx, project); err != nil {
					t.Fatal(err)
				}
				service := runtime.Execution()
				parent, err := service.StartRun(ctx, goal.SessionID, project)
				if err != nil {
					t.Fatal(err)
				}
				base := repo.HEAD(t)
				if err := service.Engine().SetTaskLaneParent(ctx, parent.RunID, base); err != nil {
					t.Fatal(err)
				}
				lane, err := service.Engine().OpenTaskLane(ctx, parent.RunID, "source", base)
				if err != nil {
					t.Fatal(err)
				}
				laneRunID = lane.RunID
				run, err := service.ExecuteTaskBound(ctx, lane.RunID, "source", base)
				if invalidate {
					task := run.Tasks["source"]
					if err == nil || task.State == execution.TaskCompletedPendingVerify || task.ResultCommit != "" {
						t.Fatalf("deliberately invalidated worker qualified: state=%s commit=%q err=%v", task.State, task.ResultCommit, err)
					}
					if !errors.Is(err, projectcontext.ErrChanged) && !strings.Contains(task.LastFailureReason, "REQUIRED_CONTEXT") {
						t.Fatalf("unexpected invalidation outcome: state=%s reason=%q err=%v", task.State, task.LastFailureReason, err)
					}
				} else if err != nil || run.Tasks["source"].State != execution.TaskCompletedPendingVerify {
					t.Fatalf("actual Process05 source worker failed: %v reason=%q", err, run.Tasks["source"].LastFailureReason)
				}
			}
			if callCapturedKnowledge(t, ctx, host, map[string]any{"query": "SourceAnchor"}) {
				t.Fatal("closed worker retained source authority")
			}
		})
	}
}
