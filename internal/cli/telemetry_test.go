package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/cloud"
)

func TestTelemetryCommandsPersistAcrossProjects(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv(cloud.EnvEndpoint, "off")
	run := func(root string, args ...string) string {
		t.Helper()
		var out, errout bytes.Buffer
		if code := Execute(context.Background(), root, args, nil, &out, &errout); code != 0 {
			t.Fatalf("%v: %d %s", args, code, errout.String())
		}
		return out.String()
	}
	first, second := t.TempDir(), t.TempDir()
	if out := run(first, "telemetry", "status"); !strings.Contains(out, "off (MARSHAL_CLOUD_ENDPOINT=off; nothing is sent)") {
		t.Fatal(out)
	}
	store := cloud.NewStore(filepath.Join(config, "marshal"))
	before, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	run(first, "telemetry", "disable")
	var status struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.Unmarshal([]byte(run(second, "--json", "telemetry", "status")), &status); err != nil || status.Enabled {
		t.Fatal("choice did not persist", err)
	}
	run(second, "telemetry", "disable")
	disabled, _ := store.Load()
	if disabled.InstallationID != before.InstallationID {
		t.Fatal("repeat disable changed identity")
	}
	run(second, "telemetry", "enable")
	after, _ := store.Load()
	if after.InstallationID == before.InstallationID || after.TelemetryDisabled {
		t.Fatal("enable did not replace opted-out identity")
	}
}

func TestTelemetryStatus(t *testing.T) {
	for _, tc := range []struct {
		name     string
		endpoint string
		disabled bool
		want     string
		enabled  bool
	}{
		{"endpoint off", "off", false, "Anonymous telemetry: off (MARSHAL_CLOUD_ENDPOINT=off; nothing is sent).\n", false},
		{"user disabled", "https://cloud.example.test/private", true, "Anonymous telemetry: disabled.\n", false},
		{"user disabled and endpoint off", "off", true, "Anonymous telemetry: disabled.\n", false},
		{"enabled", "https://user:secret@cloud.example.test:8443/private?token=secret#fragment", false, "Anonymous telemetry: enabled (cloud.example.test:8443).\n", true},
		{"default endpoint", "", false, "Anonymous telemetry: enabled (marshal.blackhat.uz).\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", config)
			t.Setenv(cloud.EnvEndpoint, tc.endpoint)
			if _, _, err := cloud.NewStore(filepath.Join(config, "marshal")).SetTelemetryEnabled(t.Context(), !tc.disabled); err != nil {
				t.Fatal(err)
			}
			for _, jsonOutput := range []bool{false, true} {
				var out, errout bytes.Buffer
				args := []string{"telemetry", "status"}
				if jsonOutput {
					args = append([]string{"--json"}, args...)
				}
				if code := Execute(t.Context(), t.TempDir(), args, nil, &out, &errout); code != 0 {
					t.Fatalf("%v: exit %d: %s", args, code, errout.String())
				}
				if jsonOutput {
					var status struct {
						Enabled bool `json:"enabled"`
					}
					if err := json.Unmarshal(out.Bytes(), &status); err != nil {
						t.Fatal(err)
					}
					if status.Enabled != tc.enabled {
						t.Fatalf("enabled = %v, want %v", status.Enabled, tc.enabled)
					}
				} else if out.String() != tc.want {
					t.Fatalf("status = %q, want %q", out.String(), tc.want)
				}
			}
		})
	}
}

func TestTelemetryOfflineCommandDoesNotWait(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(cloud.EnvEndpoint, "http://127.0.0.1:1")
	var out, errout bytes.Buffer
	start := time.Now()
	if code := Execute(context.Background(), t.TempDir(), []string{"version"}, nil, &out, &errout); code != 0 {
		t.Fatalf("version failed: %s", errout.String())
	}
	if time.Since(start) > time.Second {
		t.Fatal("offline telemetry delayed command")
	}
}

func TestShortCommandSendsInstallationStartBeforeExit(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var events atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/telemetry/events" {
			var batch []struct {
				Kind string `json:"kind"`
			}
			if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
				t.Error(err)
			}
			for _, e := range batch {
				if e.Kind == "app_start" {
					events.Add(1)
				}
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	t.Setenv(cloud.EnvEndpoint, server.URL)
	var out, errout bytes.Buffer
	if code := Execute(context.Background(), t.TempDir(), []string{"version"}, nil, &out, &errout); code != 0 {
		t.Fatalf("version failed: %s", errout.String())
	}
	if got := events.Load(); got != 1 {
		t.Fatalf("app_start events before command exit = %d, want 1", got)
	}
}

func TestTUIChildDoesNotStartInstallationTelemetry(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv(cloud.EnvEndpoint, "http://127.0.0.1:1")
	c := command{}
	ctx, stop := c.startTelemetry(context.Background(), []string{"tui", telemetryChildFlag})
	stop()
	if _, ok := ctx.Value(telemetryModeKey{}).(*cloud.ActiveMode); !ok {
		t.Fatal("TUI child lost its active mode")
	}
}

func TestTelemetryHelpDoesNotCreateIdentity(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv(cloud.EnvEndpoint, "http://127.0.0.1:1")
	for _, args := range [][]string{{"telemetry", "--help"}, {"ultra", "request", "--help"}} {
		var out, errout bytes.Buffer
		if Execute(context.Background(), t.TempDir(), args, nil, &out, &errout) != 0 || !strings.Contains(out.String(), "Usage:") {
			t.Fatalf("help failed %v: %s", args, errout.String())
		}
	}
	if _, err := cloud.NewStore(filepath.Join(config, "marshal")).Load(); err == nil {
		t.Fatal("help created an identity")
	}
}

func TestCloudClientVersionNeverSendsBuildText(t *testing.T) {
	for _, v := range []string{"dev", "dev-deadbeef-dirty", "/home/private/repo", "1.0.0 extra"} {
		if got := cloudClientVersion(v); got != "0.0.0-dev" {
			t.Fatalf("build version %q sent as %q", v, got)
		}
	}
}
