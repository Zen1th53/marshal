package cli

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"

	"github.com/Zen1th53/marshal/internal/cloud"
	"github.com/Zen1th53/marshal/internal/projectid"
)

type telemetryModeKey struct{}

func (c *command) machineCloudDir() (string, error) {
	return cloud.MachineStateDir(filepath.Join(c.root, projectid.StateDirName))
}

func (c *command) startTelemetry(ctx context.Context, args []string) (context.Context, context.CancelFunc) {
	mode := &cloud.ActiveMode{}
	ctx = context.WithValue(ctx, telemetryModeKey{}, mode)
	noop := func() {}
	// A control-center launcher owns this user-visible start. Its tmux child
	// still needs an ActiveMode for authorization, but must not report a second
	// start for the same launch.
	if len(args) > 1 && args[0] == "tui" && args[1] == telemetryChildFlag {
		return ctx, noop
	}
	// Preference commands must not race an app_start against disable.
	if len(args) > 0 && args[0] == "telemetry" {
		return ctx, noop
	}
	cfg := cloud.LoadConfig()
	if !cfg.Enabled() {
		return ctx, noop
	}
	dir, err := c.machineCloudDir()
	if err != nil {
		return ctx, noop
	}
	store := cloud.NewStore(dir)
	if !c.json && c.isInteractiveTerminal() && store.TakeTelemetryNotice(ctx) {
		fmt.Fprintln(c.stderr, "Anonymous telemetry is on: random ID/public key, version, OS/arch, mode and start/heartbeat times; no project content. Disable: marshal telemetry disable.")
	}
	return ctx, cloud.StartInstallationTelemetry(ctx, cfg, dir, cloudClientVersion(Version), mode)
}

func (c *command) telemetry(ctx context.Context, args []string) error {
	if len(args) != 1 || (args[0] != "status" && args[0] != "enable" && args[0] != "disable") {
		return fmt.Errorf("usage: marshal telemetry status|disable|enable")
	}
	dir, err := c.machineCloudDir()
	if err != nil {
		return err
	}
	store := cloud.NewStore(dir)
	st, err := store.TelemetryPreference(ctx)
	if err != nil {
		return err
	}
	changed := false
	if args[0] != "status" {
		st, changed, err = store.SetTelemetryEnabled(ctx, args[0] == "enable")
		if err != nil {
			return err
		}
	}
	cfg := cloud.LoadConfig()
	if changed && st.TelemetryDisabled && cfg.Enabled() {
		if client, err := cloud.NewClient(cfg.Endpoint, cloudClientVersion(Version)); err == nil {
			client.SendOptOut(ctx, st)
		}
	}
	status := "enabled"
	switch {
	case st.TelemetryDisabled:
		status = "disabled"
	case !cfg.Enabled():
		status = "off (MARSHAL_CLOUD_ENDPOINT=off; nothing is sent)"
	default:
		if endpoint, err := url.Parse(cfg.Endpoint); err == nil && endpoint.Host != "" {
			status += " (" + endpoint.Host + ")"
		}
	}
	detail := "Anonymous telemetry: " + status + "."
	if changed && !st.TelemetryDisabled {
		detail += " A new installation identity and keypair were created; request ULTRA again if needed."
	}
	return c.print(struct {
		Enabled bool `json:"enabled"`
	}{cfg.Enabled() && !st.TelemetryDisabled}, detail)
}

func observeCloudAuthorization(ctx context.Context, a cloud.Authorization) {
	if mode, ok := ctx.Value(telemetryModeKey{}).(*cloud.ActiveMode); ok {
		mode.Attach(a.Gate)
	}
}
func observeULTRAExecution(ctx context.Context, enabled func() bool) {
	if mode, ok := ctx.Value(telemetryModeKey{}).(*cloud.ActiveMode); ok {
		mode.ObserveExecution(enabled)
	}
}
