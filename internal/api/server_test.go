package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/testutil/testgit"
)

func TestUnixServerVersionStatusAndLifecycle(t *testing.T) {
	runtime, socket := apiRuntime(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- NewServer(runtime).Serve(ctx, socket) }()
	waitForSocket(t, socket, done)
	info, err := os.Stat(socket)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode = %o", info.Mode().Perm())
	}

	client := NewClient(socket)
	version, requestID, err := client.Version(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if version.APIVersion != "v1" || requestID == "" {
		t.Fatalf("version=%#v request_id=%q", version, requestID)
	}
	status, _, err := client.Status(context.Background())
	if err != nil || status.SchemaVersion != 7 {
		t.Fatalf("status=%#v err=%v", status, err)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop")
	}
	if _, err := os.Stat(socket); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket survived shutdown: %v", err)
	}
}

func TestUnixServerTaskFlowAndTypedConflict(t *testing.T) {
	runtime, socket := apiRuntime(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- NewServer(runtime).Serve(ctx, socket) }()
	waitForSocket(t, socket, done)
	client := NewClient(socket)

	agent, _, err := client.RegisterAgent(context.Background(), app.RegisterAgentRequest{
		Name: "codex", Role: model.RoleDeveloper,
	})
	if err != nil {
		t.Fatal(err)
	}
	imported, _, err := client.ImportTasks(context.Background(), []model.Task{{
		ID: "TASK-001", Title: "runtime", Status: model.TaskReady, Risk: model.R1,
	}})
	if err != nil || imported.Added != 1 {
		t.Fatalf("import=%#v err=%v", imported, err)
	}
	claim, _, err := client.Claim(context.Background(), app.ClaimRequest{
		TaskID: "TASK-001", AgentID: agent.ID, ExpectedRevision: 0,
	})
	if err != nil || claim.Lease.ID == "" {
		t.Fatalf("claim=%#v err=%v", claim, err)
	}
	_, _, err = client.Claim(context.Background(), app.ClaimRequest{
		TaskID: "TASK-001", AgentID: agent.ID, ExpectedRevision: 0,
	})
	if !errors.Is(err, model.ErrConflict) {
		t.Fatalf("second claim error = %v", err)
	}
	if _, err := client.Release(context.Background(), app.ReleaseRequest{TaskID: "TASK-001"}); err != nil {
		t.Fatal(err)
	}
}

func TestUnixServerRejectsMalformedAndOversizedJSON(t *testing.T) {
	runtime, socket := apiRuntime(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- NewServer(runtime).Serve(ctx, socket) }()
	waitForSocket(t, socket, done)
	client := NewClient(socket)

	for _, body := range []string{`{"name":`, `{"name":"x","role":"developer","unknown":1}`, `{"name":"` + strings.Repeat("x", 1<<20) + `","role":"developer"}`} {
		request, err := http.NewRequest(http.MethodPost, "http://unix/v1/agents", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.HTTP().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusBadRequest && response.StatusCode != http.StatusRequestEntityTooLarge {
			t.Fatalf("body length %d: status %d", len(body), response.StatusCode)
		}
	}
}

func TestPrepareSocketRefusesUnknownLiveSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.sock")
	address := &net.UnixAddr{Name: path, Net: "unixgram"}
	listener, err := net.ListenUnixgram("unixgram", address)
	if err != nil {
		t.Skipf("Unix datagram unavailable: %v", err)
	}
	defer listener.Close()
	if err := prepareSocket(path); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("prepare error = %v", err)
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("unknown live socket was removed: %v", err)
	}
}

func apiRuntime(t *testing.T) (*app.Runtime, string) {
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
	layout, err := app.Bootstrap(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := app.Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runtime.Close() })
	return runtime, layout.Socket
}

func waitForSocket(t *testing.T, socket string, done <-chan error) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			t.Fatalf("server stopped before listening: %v", err)
		default:
		}
		if _, err := os.Stat(socket); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("socket %s did not appear", socket)
}
