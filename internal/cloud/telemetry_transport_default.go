package cloud

import (
	"context"
	"errors"
	"sync"
	"time"
)

const telemetryReregisterInterval = time.Hour

type telemetryReregisterKey struct {
	endpoint       string
	installationID string
}

var (
	telemetryReregisterMu   sync.Mutex
	telemetryReregisterLast = map[telemetryReregisterKey]time.Time{}
	telemetryNow            = time.Now
)

// claimTelemetryReregister allows at most one telemetry-triggered
// re-registration per process per hour for this endpoint and installation.
func claimTelemetryReregister(endpoint, installationID string) bool {
	telemetryReregisterMu.Lock()
	defer telemetryReregisterMu.Unlock()
	now := telemetryNow()
	key := telemetryReregisterKey{endpoint: endpoint, installationID: installationID}
	if last, ok := telemetryReregisterLast[key]; ok && now.Sub(last) < telemetryReregisterInterval {
		return false
	}
	telemetryReregisterLast[key] = now
	return true
}

// telemetryReregisterOnCooldown prevents a failed forced re-registration from
// being retried by subsequent telemetry ticks more than once per process per hour.
func telemetryReregisterOnCooldown(endpoint, installationID string) bool {
	telemetryReregisterMu.Lock()
	defer telemetryReregisterMu.Unlock()
	key := telemetryReregisterKey{endpoint: endpoint, installationID: installationID}
	last, ok := telemetryReregisterLast[key]
	if !ok {
		return false
	}
	now := telemetryNow()
	if now.Sub(last) < telemetryReregisterInterval {
		return true
	}
	telemetryReregisterLast[key] = now
	return false
}

// SendTelemetry submits a batch of operational events.
//
// The server re-validates every event and drops any that fail rather than
// sanitising them, so a client bug costs the event, not the privacy guarantee.
// This is the one call whose failure is genuinely unimportant: losing telemetry
// must never cost somebody their session.
func (c *Client) SendTelemetry(ctx context.Context, events []Event) error {
	if len(events) == 0 {
		return nil
	}
	if IsProductionEndpoint(c.endpoint) && !productionAllowed() {
		return ErrDevelopmentBuild
	}
	canSend := func() (State, bool) {
		if c.store == nil {
			return State{}, true
		}
		current, err := c.store.Load()
		if err != nil || current.TelemetryDisabled {
			return State{}, false
		}
		for _, event := range events {
			if event.InstallationID != current.InstallationID {
				return State{}, false
			}
		}
		return current, true
	}
	if _, ok := canSend(); !ok {
		return nil
	}
	err := c.post(ctx, "/v1/telemetry/events", events, nil)
	if !errors.Is(err, ErrUnknownInstallation) || c.store == nil {
		return err
	}
	current, ok := canSend()
	if !ok {
		return nil
	}
	if !claimTelemetryReregister(c.endpoint, current.InstallationID) {
		return err
	}
	if _, registerErr := c.ensureRegisteredFor(ctx, current, true, true); registerErr != nil {
		return registerErr
	}
	if _, ok := canSend(); !ok {
		return nil
	}
	return c.post(ctx, "/v1/telemetry/events", events, nil)
}
