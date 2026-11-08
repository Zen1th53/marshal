//go:build linux

package tui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/cloud"
)

func TestPTYEntitlementRequestTracking(t *testing.T) {
	if !editionExtreme {
		t.Skip("candidate navigation qualification requires (default build)")
	}
	openPTY(t)
	if _, err := exec.LookPath("codex"); err != nil {
		if _, err := exec.LookPath("claude"); err != nil {
			t.Skip("ULTRA execution preference needs an installed provider CLI")
		}
	}
	if _, ok := any(&app.Runtime{}).(ultraRequestServiceProvider); !ok {
		t.Skip("integration prerequisite: compose typed persistent request service and install ultraRequestActionBindings")
	}
	fixture := newLocalCloudFixture(t)
	fixture.refuseLeases(true)
	var calls atomic.Int32
	keys := make(chan cloud.RequestKey, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/entitlements/request" {
			fixture.srv.Config.Handler.ServeHTTP(w, r)
			return
		}
		var body struct {
			InstallationID string `json:"installation_id"`
			SessionID      string `json:"session_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid", 400)
			return
		}
		calls.Add(1)
		keys <- cloud.RequestKey{Endpoint: "http://" + r.Host, InstallationID: body.InstallationID, SessionID: body.SessionID}
		json.NewEncoder(w).Encode(cloud.EntitlementResponse{Status: "pending", Tier: "ultra"})
	}))
	defer server.Close()
	bin := buildMarshalBinary(t)
	sweepWorkEnvironment(t)
	project := initProject(t, bin)
	configHome := t.TempDir()
	terminal := startTUIInProjectEnv(t, 40, 160, bin, project, map[string]string{"MARSHAL_CLOUD_ENDPOINT": server.URL, "MARSHAL_NO_UPDATE_CHECK": "1", "XDG_CONFIG_HOME": configHome, "MARSHAL_PROVIDER_PATH_ONLY": "1"}, "tui", "SESSION-request-tracking")
	terminal.send("\x0e")
	if editionExtreme {
		// Extreme runs ULTRA locally and never creates a cloud client, so the
		// request action is not offered at all: the neighbouring ULTRA mode
		// actions are still there, and the configured endpoint never receives
		// a request.
		recoveryMenuPTYAction(terminal, "ULTRA execution preference")
		terminal.mustSee("Confirm")
		terminal.send("\x1b")
		if calls.Load() != 0 {
			t.Fatalf("Marshal sent %d entitlement requests", calls.Load())
		}
		return
	}
	recoveryMenuPTYAction(terminal, "Request ULTRA entitlement")
	terminal.mustSee("Proceed")
	terminal.send("\x1b[C\r")
	terminal.mustSee("Local attempt")
	terminal.mustSee("server request ID UNKNOWN")
	terminal.mustSee("grants no ULTRA lease")
	terminal.send("\r\r")
	if calls.Load() != 1 {
		t.Fatalf("duplicate input sent %d requests", calls.Load())
	}
	key := <-keys
	// The integrator roots request state alongside the machine installation
	// identity, never a production config directory or the test's source tree.
	requests := cloud.NewRequestStore(filepath.Join(configHome, "marshal"))
	proof, found, err := requests.Read(context.Background(), key)
	if err != nil || !found || proof.State != "pending" || proof.LocalAttemptID == "" || proof.ServerRequestID != "" {
		t.Fatalf("durable request readback: %+v %v %v", proof, found, err)
	}
	if err = terminal.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = terminal.cmd.Wait()
	reopened := cloud.NewRequestStore(filepath.Join(configHome, "marshal"))
	after, found, err := reopened.Read(context.Background(), key)
	if err != nil || !found || after.LocalAttemptID != proof.LocalAttemptID {
		t.Fatalf("request lost across crash: %+v %v %v", after, found, err)
	}
}
