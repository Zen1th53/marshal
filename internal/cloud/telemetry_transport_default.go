package cloud

import "context"

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
	if c.store != nil {
		current, err := c.store.Load()
		if err != nil || current.TelemetryDisabled {
			return nil
		}
		for _, event := range events {
			if event.InstallationID != current.InstallationID {
				return nil
			}
		}
	}
	return c.post(ctx, "/v1/telemetry/events", events, nil)
}
