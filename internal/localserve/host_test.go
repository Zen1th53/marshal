package localserve

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/api"
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/project"
	"github.com/Zen1th53/marshal/internal/testutil/testgit"
)

func openRuntime(t *testing.T) *app.Runtime {
	t.Helper()
	repo := testgit.New(t)
	for _, name := range []string{"CAPABILITIES.yaml", "PACK-VERSION.yaml", "RUNTIME-VERSION.yaml"} {
		data, err := os.ReadFile(filepath.Join("..", "..", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo.Path(), name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := app.Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	runtime, err := app.Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	return runtime
}

func TestHTTPServersAreAuthenticatedLoopbackAndStopOnClose(t *testing.T) {
	host := New(openRuntime(t))
	t.Cleanup(host.Close)
	ctx := context.Background()
	if _, err := host.Start(ctx, MCP, "0.0.0.0:0"); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("non-loopback bind: %v", err)
	}
	mcpStatus, err := host.Start(ctx, MCP, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Start(ctx, MCP, "127.0.0.1:0"); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("second start: %v", err)
	}
	resp, err := http.Post("http://"+mcpStatus.Address+"/", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden {
		t.Fatalf("unauthenticated MCP request got %d", resp.StatusCode)
	}
	a2aStatus, err := host.Start(ctx, A2A, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := host.Status(A2A); !ok || got.Address != a2aStatus.Address {
		t.Fatalf("status: %+v %v", got, ok)
	}
	host.Close()
	if _, err := http.Get("http://" + a2aStatus.Address + "/.well-known/agent-card.json"); err == nil {
		t.Fatal("A2A server still accepts connections after Close")
	}
	if _, err := host.Start(ctx, MCP, "127.0.0.1:0"); !errors.Is(err, model.ErrUnavailable) {
		t.Fatalf("start after close: %v", err)
	}
}

func TestDaemonServesTheRuntimeSocketAndClearsItsPIDFile(t *testing.T) {
	runtime := openRuntime(t)
	host := New(runtime)
	t.Cleanup(host.Close)
	status, err := host.Start(context.Background(), Daemon, "")
	if err != nil {
		t.Fatal(err)
	}
	value, _, err := api.NewClient(status.Address).Status(context.Background())
	if err != nil || value.SchemaVersion == 0 {
		t.Fatalf("CLI client against the hosted socket: %+v %v", value, err)
	}
	other := New(runtime)
	if _, err := other.Start(context.Background(), Daemon, ""); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("second daemon: %v", err)
	}
	host.Close()
	layout, err := project.Discover(runtime.ProjectRoot())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(layout.PID); !os.IsNotExist(err) {
		t.Fatalf("PID file left behind: %v", err)
	}
}
