package app

import "fmt"

// ProviderDisabledError is a dispatch refused because project settings turn
// off the task's provider and no enabled provider can take the task. It
// carries a stop reason so the Board names the setting instead of reporting
// an unexpected stop.
type ProviderDisabledError struct {
	Provider string
	Cause    error
}

func (e *ProviderDisabledError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%v; provider %s is disabled", e.Cause, e.Provider)
	}
	return fmt.Sprintf("provider %s is disabled by project settings", e.Provider)
}

func (e *ProviderDisabledError) Unwrap() error { return e.Cause }

// StopReason implements the Board's stop reason.
func (e *ProviderDisabledError) StopReason() (string, string) {
	return fmt.Sprintf("Provider %s is turned off in project settings and no enabled provider can take this task.", e.Provider),
		"Turn the provider back on in Settings (or enable another provider for this mode), then press Enter to continue."
}
