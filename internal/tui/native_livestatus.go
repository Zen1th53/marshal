package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// What capture is doing, while it is doing it.
//
// Memory capture has always been live — the watcher imports every couple of
// seconds — but nothing said so. The only report came after the child exited
// ("N message(s) saved"), which reads as though that is when saving happened,
// and an operator watching a long session had no way to tell a working capture
// from a stalled one.
//
// MARSHAL cannot draw this: the native CLI owns the terminal for the whole
// session, and anything MARSHAL printed would land in the middle of it. So the
// count goes to a file the operator, or the agent itself, can read whenever
// they want to know.

// liveStatus is what a session publishes about its own capture.
type liveStatus struct {
	Provider string `json:"provider"`
	// Imported counts memory records written so far this session, not messages
	// seen: a message already captured under a previous launch is not counted
	// again, and the difference is the point of saying "records".
	Imported int `json:"imported"`
	// Delivered counts entries written into other providers' inboxes.
	Delivered int `json:"delivered"`
	// LastSync is when the watcher last completed a pass, so a stalled capture
	// shows as a timestamp that stops moving rather than as silence.
	LastSync time.Time `json:"last_sync"`
	// Error carries the most recent capture failure, empty when the last pass
	// succeeded. A session that is failing to save should not look idle.
	Error string `json:"error,omitempty"`
}

func liveStatusPath(root, provider string) string {
	return filepath.Join(root, ".marshal", provider, "live-status.json")
}

// writeLiveStatus publishes the current capture state.
//
// A failure to write is returned but never stops a session: this file is a
// report about the work, not the work.
func writeLiveStatus(root, provider string, status liveStatus) error {
	status.Provider = provider
	path := liveStatusPath(root, provider)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return err
	}
	// Replace whole, so a reader never sees half a document.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// readLiveStatus reports what a provider's last session published, for the
// operator asking after the fact.
func readLiveStatus(root, provider string) (liveStatus, error) {
	data, err := os.ReadFile(liveStatusPath(root, provider))
	if err != nil {
		return liveStatus{}, err
	}
	var status liveStatus
	if err := json.Unmarshal(data, &status); err != nil {
		return liveStatus{}, fmt.Errorf("parse %s live status: %w", provider, err)
	}
	return status, nil
}
