package a2a

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/app"
)

func TestSendMessageBindsWorkerToSelectedProvider(t *testing.T) {
	for _, provider := range []string{"codex", "claude", "opencode", ""} {
		t.Run(provider, func(t *testing.T) {
			repo := runtimeRepo(t)
			ctx := context.Background()
			if _, err := app.Bootstrap(ctx, repo.Path()); err != nil {
				t.Fatal(err)
			}
			rt, err := app.Open(ctx, repo.Path())
			if err != nil {
				t.Fatal(err)
			}
			defer rt.Close()
			// No credential grant: dispatch must stop before making a paid call.
			request := httptest.NewRequest(http.MethodPost, "/message:send", strings.NewReader(`{"message":{"message_id":"binding-proof","role":"ROLE_USER","parts":[{"text":"Create provider-proof.txt"}]},"adapter":"`+provider+`"}`))
			request.RemoteAddr = "127.0.0.1:1234"
			recorder := httptest.NewRecorder()
			mustInsecureServer(t, rt).Handler().ServeHTTP(recorder, request)
			if recorder.Code != http.StatusOK {
				t.Fatalf("dispatch: status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			agents, err := rt.Agents(ctx)
			if err != nil || len(agents) != 1 {
				t.Fatalf("agents=%v err=%v", agents, err)
			}
			want := provider
			if want == "" {
				want = "codex"
			}
			if agents[0].ModelProvider != want {
				t.Fatalf("registered worker provider=%q, want %q", agents[0].ModelProvider, want)
			}
		})
	}
}

type failedProvider struct{}

func (failedProvider) Probe(context.Context) (adapter.Probe, error) {
	return adapter.Probe{Name: "codex", Version: "fixture-1", Available: true}, nil
}
func (failedProvider) Run(context.Context, adapter.Request) (adapter.Result, error) {
	return adapter.Result{Status: adapter.StatusFailure, ExitCode: 7, Stderr: []byte("fixture provider failure")}, nil
}
func (failedProvider) Status(context.Context, string) (adapter.Status, error) {
	return adapter.StatusFailure, nil
}
func (failedProvider) Resume(context.Context, string, adapter.Request) (adapter.Result, error) {
	return adapter.Result{Status: adapter.StatusFailure, ExitCode: 7}, nil
}
func (failedProvider) Capabilities() map[string]string               { return nil }
func (failedProvider) CollectEvidence(adapter.Result) map[string]any { return nil }
func (failedProvider) Shutdown(context.Context, string) error        { return nil }

func TestSendMessageDoesNotCompleteFailedWorker(t *testing.T) {
	repo := runtimeRepo(t)
	ctx := context.Background()
	if _, err := app.Bootstrap(ctx, repo.Path()); err != nil {
		t.Fatal(err)
	}
	rt, err := app.OpenWithOptions(ctx, repo.Path(), app.Options{Adapters: map[string]adapter.Adapter{"codex": failedProvider{}}})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	request := httptest.NewRequest(http.MethodPost, "/message:send", strings.NewReader(`{"message":{"message_id":"failed-proof","role":"ROLE_USER","parts":[{"text":"Create provider-proof.txt"}]},"adapter":"codex"}`))
	request.RemoteAddr = "127.0.0.1:1234"
	recorder := httptest.NewRecorder()
	mustInsecureServer(t, rt).Handler().ServeHTTP(recorder, request)
	var response struct {
		State string `json:"state"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusOK || response.State != "TASK_STATE_FAILED" || response.Error == "" {
		t.Fatalf("failed worker response: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
