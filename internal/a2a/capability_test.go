package a2a

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/auth"
)

func TestTaskMutationsRequireExecute(t *testing.T) {
	ctx := context.Background()
	for _, capability := range []auth.Capability{auth.CapTaskRead, auth.CapTaskCreate, auth.CapTaskExecute} {
		t.Run(string(capability), func(t *testing.T) {
			repo := runtimeRepo(t)
			if _, err := app.Bootstrap(ctx, repo.Path()); err != nil {
				t.Fatal(err)
			}
			runtime, err := app.Open(ctx, repo.Path())
			if err != nil {
				t.Fatal(err)
			}
			defer runtime.Close()
			manager := auth.NewManager(t.TempDir())
			server := mustAuthenticatedServer(t, runtime, manager)
			token, _, err := manager.CreateToken("remote-agent", auth.KindA2AAgent, []string{string(capability)})
			if err != nil {
				t.Fatal(err)
			}
			for _, endpoint := range []struct{ path, body string }{
				{"/message:send", `{"task_id":"TASK-CAP-MESSAGE","adapter":"missing-adapter","message":{"message_id":"cap-message","role":"user","parts":[{"text":"Check the project"}]}}`},
				{"/a2a/tasks", `{"protocol_version":"1.0.0","sender_id":"remote-agent","task":{"id":"TASK-CAP-DELEGATE","title":"Check the project"}}`},
			} {
				req := httptest.NewRequest(http.MethodPost, endpoint.path, strings.NewReader(endpoint.body))
				req.Header.Set("Authorization", "Bearer "+token)
				response := httptest.NewRecorder()
				server.Handler().ServeHTTP(response, req)
				if capability == auth.CapTaskExecute {
					if response.Code != http.StatusOK {
						t.Fatalf("%s: code=%d body=%s", endpoint.path, response.Code, response.Body.String())
					}
				} else if response.Code != http.StatusForbidden {
					t.Errorf("%s: expected forbidden, got %d: %s", endpoint.path, response.Code, response.Body.String())
				}
			}
			tasks, err := runtime.Tasks(ctx)
			if err != nil {
				t.Fatal(err)
			}
			agents, err := runtime.Agents(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if capability != auth.CapTaskExecute && (len(tasks) != 0 || len(agents) != 0) {
				t.Fatalf("denied requests changed state: tasks=%d agents=%d", len(tasks), len(agents))
			}
			if capability == auth.CapTaskExecute && (len(tasks) != 2 || len(agents) != 1) {
				t.Fatalf("execute requests did not reach mutations: tasks=%d agents=%d", len(tasks), len(agents))
			}
		})
	}
}
