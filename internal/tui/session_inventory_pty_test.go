//go:build linux

package tui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/memory/importer"
	"github.com/Zen1th53/marshal/internal/model"
)

func seedNativeInventory(t *testing.T, root string, fixtures map[string]string) {
	t.Helper()
	rt, err := app.Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	for provider, id := range fixtures {
		data, err := json.Marshal(importer.SessionTranscript{SessionID: id, Provider: provider, Messages: []importer.Message{{Role: "user", Content: "Inventory fixture visible text for " + provider, Timestamp: time.Now().UTC()}}})
		if err != nil {
			t.Fatal(err)
		}
		principal := authz.Principal{ID: "inventory-fixture", Role: authz.Role{Name: "developer", Authorities: []authz.Authority{authz.AuthorityTaskPlan}}}
		if _, err := rt.Memory().ImportSessionTranscript(context.Background(), principal, rt.ProjectID(), data, false); err != nil {
			t.Fatal(err)
		}
	}
}
func seedGovernedInventory(t *testing.T, root string) {
	t.Helper()
	ctx := context.Background()
	rt, err := app.Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	st := rt.Store()
	if _, err := st.ImportTasks(ctx, []model.Task{{ID: "TASK-inventory", Title: "governed fixture", Status: model.TaskReady, Risk: model.R1}}); err != nil {
		t.Fatal(err)
	}
	if err := st.RegisterAgent(ctx, model.Agent{ID: "AGENT-inventory", ProjectID: rt.ProjectID(), DisplayName: "fixture", Role: model.RoleDeveloper, Status: model.AgentRegistered}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.StartSession(ctx, model.SessionStart{ID: "SESSION-inventory", AgentID: "AGENT-inventory", ProjectID: rt.ProjectID()}); err != nil {
		t.Fatal(err)
	}
	started := time.Now().UTC()
	if err := st.StartRun(ctx, model.WorkerRun{ID: "RUN-inventory", TaskID: "TASK-inventory", SessionID: "SESSION-inventory", Adapter: "codex", AdapterVersion: "fixture", BaseCommit: "fixture", Status: "running", StartedAt: started}); err != nil {
		t.Fatal(err)
	}
	exit := 0
	if err := st.FinishRun(ctx, model.RunFinish{ID: "RUN-inventory", Status: "success", ExitStatus: &exit, EndedAt: started.Add(time.Second), ExpectedRevision: 0}); err != nil {
		t.Fatal(err)
	}
}

func TestSessionInventoriesPTY(t *testing.T) {
	bin := buildMarshalBinary(t)
	sweepCrosscutEnvironment(t)
	log := sweepAgentCodexDouble(t)
	root := initProject(t, bin)
	seedNativeInventory(t, root, map[string]string{"codex": "old-native"})
	seedNativeInventory(t, root, map[string]string{"codex": "latest-native", "claude": "claude-native"})
	seedGovernedInventory(t, root)
	other := initProject(t, bin)
	seedNativeInventory(t, other, map[string]string{"codex": "foreign-native"})
	cases := []struct{ line, want, argv string }{
		{"/sessions", "GOVERNED runs (1)", ""},
		{"/codex sessions", "transcript imported; resumability unverified", ""},
		{"/resume RUN-inventory", "governed run/task/session ID", ""},
		{"/fork SESSION-inventory", "governed run/task/session ID", ""},
		{"/resume " + app.NativeConversationID("claude", root, "claude-native"), "another provider", ""},
		{"/resume " + app.NativeConversationID("codex", other, "foreign-native"), "another project", ""},
		{"/resume --last", "Codex exited.", "resume\nlatest-native\n"},
		{"/fork " + app.NativeConversationID("codex", root, "old-native"), "Codex exited.", "fork\nold-native\n"},
	}
	for _, tc := range cases {
		t.Run(tc.line, func(t *testing.T) {
			s := startFrozenTUIInProject(t, 50, 220, bin, root, "tui")
			_ = os.Remove(log)
			s.send(tc.line)
			s.send("\x1b")
			s.send("\r")
			s.mustSee(tc.want)
			if tc.line == "/sessions" {
				s.mustSee("NATIVE conversations (3)")
				s.mustSee("RUN-inventory")
				s.mustSee("latest-native")
				s.mustSee("provider=claude")
			}
			if tc.argv != "" {
				data, err := os.ReadFile(log)
				if err != nil || !strings.HasSuffix(string(data), tc.argv) {
					t.Fatalf("argv=%q want suffix=%q %v", data, tc.argv, err)
				}
				s.mustSee("Resumability unverified")
			} else if strings.Contains(tc.want, "another ") || strings.Contains(tc.want, "governed run/task") {
				if data, err := os.ReadFile(log); !os.IsNotExist(err) {
					t.Fatalf("refused selection launched provider: %q %v", data, err)
				}
			}
		})
	}
	// Project imports are durable, and no provider-owned state was manufactured.
	if _, err := os.Stat(filepath.Join(os.Getenv("CODEX_HOME"), "sessions")); !os.IsNotExist(err) {
		t.Fatalf("imports manufactured provider history: %v", err)
	}
}
