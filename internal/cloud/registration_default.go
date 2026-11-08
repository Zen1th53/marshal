package cloud

import (
	"context"
	"encoding/base64"
	"path/filepath"
	"runtime"
)

// ensureRegistered enrolls only when this identity is not known at this endpoint.
func (c *Client) ensureRegistered(ctx context.Context, st State, force bool) (bool, error) {
	c.registrationMu.Lock()
	defer c.registrationMu.Unlock()
	if c.store == nil {
		return false, nil
	}
	// Serialize enrollment separately from preferences, so a background network
	// attempt never holds up the interactive notice or preference commands.
	ctx, cancel := context.WithTimeout(ctx, telemetryTimeout)
	defer cancel()
	registered := false
	key := RequestKey{Endpoint: c.endpoint, InstallationID: st.InstallationID, SessionID: "sess-1111111111111111"}
	err := NewRequestStore(filepath.Dir(c.store.path)).locked(ctx, key, func(string) error {
		current, err := c.store.Load()
		if err != nil {
			return err
		}
		if current.InstallationID != st.InstallationID || (c.telemetryOnly && current.TelemetryDisabled) {
			return ErrState
		}
		if !force && current.RegisteredInstallationID == st.InstallationID && current.RegisteredEndpoint == c.endpoint {
			return nil
		}
		if force {
			if err := c.store.withState(ctx, func() error {
				latest, err := c.store.Load()
				if err != nil {
					return err
				}
				if latest.InstallationID != st.InstallationID {
					return ErrState
				}
				latest.RegisteredInstallationID, latest.RegisteredEndpoint = "", ""
				return c.store.Save(latest)
			}); err != nil {
				return err
			}
		}
		if err := c.Register(ctx, st); err != nil {
			return err
		}
		return c.store.withState(ctx, func() error {
			latest, err := c.store.Load()
			if err != nil {
				return err
			}
			if latest.InstallationID != st.InstallationID {
				return ErrState
			}
			latest.RegisteredInstallationID, latest.RegisteredEndpoint = st.InstallationID, c.endpoint
			if err := c.store.Save(latest); err != nil {
				return err
			}
			registered = true
			return nil
		})
	})
	return registered, err
}

// Register enrolls this installation and returns the server's view of it.
func (c *Client) Register(ctx context.Context, st State) error {
	pub, err := st.Public()
	if err != nil {
		return err
	}
	// Platform is sent because the server validates registration through the
	// same rules as telemetry, and those require a recognised os and arch.
	// These describe the build, not the machine: "linux/amd64" says nothing
	// about who is running it.
	return c.post(ctx, "/v1/installations/register", map[string]any{
		"installation_id": st.InstallationID,
		"public_key":      base64.RawURLEncoding.EncodeToString(pub),
		"client_version":  c.version,
		"os":              runtime.GOOS,
		"arch":            runtime.GOARCH,
	}, nil)
}

func registrationRateLimited(path string, status int) bool {
	return status == 429 && path == "/v1/installations/register"
}
