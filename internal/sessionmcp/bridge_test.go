//go:build linux

package sessionmcp_test

import (
	"context"
	"encoding/json"
	"github.com/Zen1th53/marshal/internal/statepath"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/adapter/claude"
	"github.com/Zen1th53/marshal/internal/adapter/codex"
	"github.com/Zen1th53/marshal/internal/adapter/opencode"
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectknowledge"
	"github.com/Zen1th53/marshal/internal/sandbox"
	"github.com/Zen1th53/marshal/internal/sessionmcp"
	"github.com/Zen1th53/marshal/internal/testutil/testgit"
	"github.com/Zen1th53/marshal/internal/worker"
	"github.com/Zen1th53/marshal/internal/worktree"
)

func TestGovernedSandboxMCPReadOnlyIPC(t *testing.T) {
	backend := sandbox.NewBwrap("/usr/bin/bwrap")
	if capability := backend.Probe(context.Background()); !capability.Available {
		t.Skip(capability.Reason)
	}
	_, source, _, _ := runtime.Caller(0)
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(source), "../.."))
	binary := filepath.Join(t.TempDir(), "marshal")
	buildArgs := []string{"build"}
	if !isExtremeBuild {
		buildArgs = append(buildArgs, "-tags", "governed")
	}
	buildArgs = append(buildArgs, "-o", binary, "./cmd/marshal")
	build := exec.Command("go", buildArgs...)
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	t.Setenv("MARSHAL_MCP_BINARY", binary)
	repo := testgit.New(t)
	if err := os.WriteFile(filepath.Join(repo.Path(), "source.go"), []byte("package fixture\nfunc BoundSource(){}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "source.go"}, {"commit", "-m", "source fixture"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo.Path()
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fixture: %v %s", err, output)
		}
	}
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	rt, err := app.Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.ImportTasks(context.Background(), []model.Task{{ID: "TASK-own", Title: "own", Status: model.TaskReady, Risk: model.R1}, {ID: "TASK-other", Title: "other", Status: model.TaskReady, Risk: model.R1}}); err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	agent, err := rt.RegisterAgent(t.Context(), app.RegisterAgentRequest{Name: "source reader", Role: model.RoleDeveloper})
	if err != nil {
		t.Fatal(err)
	}
	task, err := rt.Task(t.Context(), "TASK-own")
	if err != nil {
		t.Fatal(err)
	}
	claim, err := rt.Claim(t.Context(), app.ClaimRequest{TaskID: task.ID, AgentID: agent.ID, ExpectedRevision: task.Revision})
	if err != nil {
		t.Fatal(err)
	}
	head, err := exec.Command("git", "-C", repo.Path(), "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	tree, err := worktree.New(repo.Path(), filepath.Join(repo.Path(), statepath.DirName, "worktrees")).Prepare(t.Context(), model.WorktreeRequest{TaskID: task.ID, Branch: "marshal/" + task.ID, BaseCommit: strings.TrimSpace(string(head))})
	if err != nil {
		t.Fatal(err)
	}
	task, err = rt.Task(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = rt.Store().BeginExecution(t.Context(), task.ID, claim.Session.ID, agent.ID, tree.Branch, tree.Path, tree.HEAD, task.Revision); err != nil {
		t.Fatal(err)
	}
	index, err := projectknowledge.OpenWorktree(tree.Path, repo.Path(), filepath.Join(repo.Path(), statepath.DirName))
	if err != nil {
		t.Fatal(err)
	}
	if err = index.Ensure(t.Context()); err != nil {
		t.Fatal(err)
	}
	const providerScript = `#!/usr/bin/python3
import ast,json,os,subprocess,sys
args=sys.argv[1:]
if '--mcp-config' in args:
 s=json.load(open(args[args.index('--mcp-config')+1]))['mcpServers']['marshal']; command=[s['command']]+s['args']
elif 'OPENCODE_CONFIG_CONTENT' in os.environ:
 command=json.loads(os.environ['OPENCODE_CONFIG_CONTENT'])['mcp']['marshal']['command']
else:
 config={args[i+1].split('=',1)[0]:args[i+1].split('=',1)[1] for i in range(len(args)-1) if args[i]=='-c'}
 command=[ast.literal_eval(config['mcp_servers.marshal.command'])]+json.loads(config['mcp_servers.marshal.args'])
p=subprocess.Popen(command,stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
requests=[('find_symbol',{'query':'BoundSource'}),('find_symbol',{'task_id':'TASK-other','query':'BoundSource'}),('memory_remember',{'title':'deny','body':'deny'}),('memory_recall',{'project_id':'foreign','query':'deny'}),('tools/list',{})]
results=[]
for i,(name,arguments) in enumerate(requests):
 request={'jsonrpc':'2.0','id':i+1,'method':'tools/call','params':{'name':name,'arguments':arguments}}
 if name=='tools/list': request={'jsonrpc':'2.0','id':i+1,'method':'tools/list'}
 p.stdin.write(json.dumps(request)+'\n');p.stdin.flush()
 line=p.stdout.readline()
 if not line: raise Exception('relay exited: '+p.stderr.read())
 results.append(json.loads(line))
p.terminate();p.wait(timeout=5)
print(json.dumps(results))
`
	for _, provider := range []string{"codex", "claude", "opencode"} {
		t.Run(provider, func(t *testing.T) {
			tree := tree.Path
			fake := filepath.Join(tree, provider)
			if err := os.WriteFile(fake, []byte(providerScript), 0700); err != nil {
				t.Fatal(err)
			}
			process := worker.New(30*time.Second, time.Second, 1<<20)
			runner := worker.NewObservedSandboxed(process, backend, model.SandboxRequest{Worktree: tree}, func(ctx context.Context, refusal sandbox.Refusal) error { return nil })
			var client adapter.Adapter
			switch provider {
			case "codex":
				client = codex.NewWithModels(fake, runner, []codex.ModelInfo{{Slug: "test"}}, "test")
			case "claude":
				client = claude.New(fake, runner)
			case "opencode":
				client = opencode.New(fake, runner)
			}
			ctx, cancel := context.WithTimeout(sessionmcp.WithWorker(context.Background(), repo.Path(), "TASK-own", claim.Lease.ID), 40*time.Second)
			defer cancel()
			result, err := client.Run(ctx, adapter.Request{TaskID: "TASK-own", Title: "work", Worktree: tree})
			if err != nil || result.ExitCode != 0 {
				t.Fatalf("run: %v code %d stdout=%s stderr=%s", err, result.ExitCode, result.Stdout, result.Stderr)
			}
			request, err := sessionmcp.SandboxRequest(ctx, model.SandboxRequest{})
			if err != nil {
				t.Fatal(err)
			}
			mounts := request.ReadOnlyBinds
			for _, mount := range mounts {
				if mount.Source == repo.Path() {
					t.Fatal("host project exposed to sandbox")
				}
				if mount.Source != binary {
					if _, err := os.Stat(mount.Source); !os.IsNotExist(err) {
						t.Fatalf("session artifact survived provider completion: %s", mount.Source)
					}
				}
			}
			var responses []struct {
				Result json.RawMessage           `json:"result"`
				Error  *struct{ Message string } `json:"error"`
			}
			if err := json.Unmarshal(result.Stdout, &responses); err != nil {
				t.Fatal(string(result.Stdout), err)
			}
			if len(responses) != 5 || responses[0].Error != nil || !strings.Contains(string(responses[0].Result), "BoundSource") {
				t.Fatal(string(result.Stdout))
			}
			for _, response := range responses[1:4] {
				if response.Error == nil {
					t.Fatal("worker widened scope: ", string(result.Stdout))
				}
			}
			tools := string(responses[4].Result)
			if strings.Contains(tools, "memory_remember") || strings.Contains(tools, "tasks_list") {
				t.Fatal("worker tool exposure: ", tools)
			}
		})
	}
}
