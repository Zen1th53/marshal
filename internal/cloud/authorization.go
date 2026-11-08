//go:build governed

package cloud

import "context"

func authorizeCloud(ctx context.Context, cfg Config, runtimeDir, clientVersion string) Authorization {
	result := Authorization{}
	if !cfg.Enabled() {
		return result
	}

	client, err := NewClient(cfg.Endpoint, clientVersion)
	if err != nil {
		result.Err = err
		return result
	}
	result.Client = client

	store := NewStore(runtimeDir)
	client.store = store
	state, err := store.TelemetryPreference(ctx)
	if err != nil {
		result.Err = err
		return result
	}
	result.State = state

	sessionID, err := NewSessionID()
	if err != nil {
		result.Err = err
		return result
	}
	result.SessionID = sessionID

	// Keys come from the server rather than being compiled in, so a rotation
	// does not require shipping a new MARSHAL. The trade is that key discovery
	// rides on TLS, which is why NewClient refuses plain HTTP.
	ring, err := client.FetchKeys(ctx)
	if err != nil {
		result.Err = err
		return result
	}

	gate := NewGate(state.InstallationID, sessionID, ring, nil)
	session := NewSession(client, gate, state, sessionID)
	var reporter *Reporter
	if !state.TelemetryDisabled && !cfg.TelemetryManaged {
		reporter = NewReporter(client, state.InstallationID, sessionID, clientVersion, nil)
	}

	// The reporter is attached to the result before the first call that can
	// fail, so a failure is itself reportable. Attaching it afterwards would
	// mean the events worth having — activation failures — were the ones the
	// client had no way to send.
	result.Reporter = reporter
	session.AttachReporter(reporter)
	reporter.Record(KindAppStart, TelemetryStandard, "")

	registered, err := client.ensureRegistered(ctx, state, false)
	if err != nil {
		reporter.Record(KindErrorClass, TelemetryStandard, ClassifyError(err))
		result.Err = err
		return result
	}
	if registered {
		reporter.Record(KindRegistration, TelemetryStandard, "")
	}

	if err := session.Start(ctx); err != nil {
		reporter.Record(KindActivationFail, TelemetryStandard, ClassifyError(err))
		result.Err = err
		return result
	}
	reporter.Record(KindActivationOK, TelemetryUltra, "")
	reporter.Record(KindModeChange, TelemetryUltra, "")

	result.Gate = gate
	result.Session = session
	result.Heartbeat = NewHeartbeat(client, gate, reporter, state, sessionID)
	return result
}
