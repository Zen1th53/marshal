package codeevidence

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Push policies (setting code_evidence.push).
const (
	// PushOff adds nothing to a brief (default). Workers pull with the tools.
	PushOff = "off"
	// PushLocator adds the locator/1 list (ranked paths, exact line ranges and
	// signature lines; no code excerpts) to the worker's data context.
	PushLocator = "locator"
	// PushGated runs the locator's cost-model planner and adds the list only
	// when its expected saving beats its token cost; otherwise nothing.
	PushGated = "gated"
)

// Invalidation policies (setting code_evidence.on_invalidated).
const (
	// OnInvalidatedAdvisory records an audit event and notes the result in
	// review and the inspector (default).
	OnInvalidatedAdvisory = "advisory"
	// OnInvalidatedReturn also returns the task for rework when evidence it
	// relied on was changed by other merged work.
	OnInvalidatedReturn = "return"
)

// Settings are the project's code evidence settings.
type Settings struct {
	Push          string `json:"push,omitempty"`
	OnInvalidated string `json:"on_invalidated,omitempty"`
	// Profile is the locator cost profile ("opus" or "codex"; "" = opus).
	Profile string `json:"profile,omitempty"`
}

// SettingsFile is the settings file name in the adapter directory.
const SettingsFile = "settings.json"

// DefaultSettings returns the defaults: no push, advisory validation.
func DefaultSettings() Settings {
	return Settings{Push: PushOff, OnInvalidated: OnInvalidatedAdvisory}
}

// Validate rejects unknown values.
func (s Settings) Validate() error {
	switch s.Push {
	case PushOff, PushLocator, PushGated:
	default:
		return fmt.Errorf("code_evidence.push must be off, locator or gated, not %q", s.Push)
	}
	switch s.OnInvalidated {
	case OnInvalidatedAdvisory, OnInvalidatedReturn:
	default:
		return fmt.Errorf("code_evidence.on_invalidated must be advisory or return, not %q", s.OnInvalidated)
	}
	switch s.Profile {
	case "", "opus", "codex":
	default:
		return fmt.Errorf("code_evidence.profile must be opus or codex, not %q", s.Profile)
	}
	return nil
}

// LoadSettings reads StateDir/code-evidence/settings.json. A missing file
// gives the defaults; a malformed one is an error, never a silent default.
func (h Host) LoadSettings() (Settings, error) {
	s := DefaultSettings()
	f, err := os.Open(filepath.Join(h.Dir(), SettingsFile))
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 4096))
	if err != nil {
		return s, err
	}
	var raw Settings
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return DefaultSettings(), fmt.Errorf("code evidence settings: %w", err)
	}
	if raw.Push != "" {
		s.Push = raw.Push
	}
	if raw.OnInvalidated != "" {
		s.OnInvalidated = raw.OnInvalidated
	}
	s.Profile = raw.Profile
	if err := s.Validate(); err != nil {
		return DefaultSettings(), err
	}
	return s, nil
}
