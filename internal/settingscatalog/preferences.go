package settingscatalog

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Preferences is one atomic user record. Audit history travels with the values.
type Preferences struct {
	Revision int64              `json:"revision"`
	Values   map[string]string  `json:"values"`
	Changes  []PreferenceChange `json:"changes"`
}
type PreferenceChange struct {
	ID       string    `json:"id"`
	Key      string    `json:"key"`
	Old      string    `json:"old"`
	New      string    `json:"new"`
	Actor    string    `json:"actor"`
	At       time.Time `json:"at"`
	Revision int64     `json:"revision"`
}

var preferencesMu sync.Mutex

func PreferencesPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "marshal", "preferences.json"), nil
}

func ReadPreferences(path string) (Preferences, error) {
	p := Preferences{Values: map[string]string{}, Changes: []PreferenceChange{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	err = json.Unmarshal(data, &p)
	if p.Values == nil {
		p.Values = map[string]string{}
	}
	return p, err
}

func preferenceDefault(key string) (string, bool) {
	switch key {
	case "update-check", "web-auto-open", "thermal-enabled", "notifications", "session-import":
		return "on", true
	case "memory-channel":
		return "auto", true
	case "display-theme":
		return "default", true
	case "display-animation":
		return "on", true
	case "thermal-trigger":
		return "90", true
	case "thermal-release":
		return "85", true
	case "thermal-window":
		return "30", true
	}
	return "", false
}

func Preference(p Preferences, key string) string {
	if v, ok := p.Values[key]; ok {
		return v
	}
	v, _ := preferenceDefault(key)
	return v
}

// ApplyPreference persists a value and its receipt together; failures preserve the old record.
func ApplyPreference(path, key, value, actor string, expected int64) (Preferences, error) {
	p, err := ReadPreferences(path)
	if err != nil {
		return p, err
	}
	return applyPreferenceRecord(path, key, value, actor, expected, p)
}

func applyPreferenceRecord(path, key, value, actor string, expected int64, p Preferences) (Preferences, error) {
	preferencesMu.Lock()
	defer preferencesMu.Unlock()

	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return p, err
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return p, err
	}
	defer directory.Close()
	if err = syscall.Flock(int(directory.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return p, fmt.Errorf("preferences are being changed: %w", err)
	}
	defer syscall.Flock(int(directory.Fd()), syscall.LOCK_UN)
	current, err := ReadScopedPreference(path, key)
	if err != nil {
		return p, err
	}
	if current.Revision != p.Revision {
		return p, fmt.Errorf("preferences changed; read them again")
	}
	if p.Revision != expected {
		return p, fmt.Errorf("preferences changed; read them again")
	}
	def, ok := preferenceDefault(key)
	if !ok {
		return p, fmt.Errorf("unknown user preference %q", key)
	}
	if actor == "" {
		return p, fmt.Errorf("missing actor")
	}
	if key == "memory-channel" {
		if !strings.Contains("|auto|off|system-prompt|project-doc|prompt|developer-instructions|marshal-dir|", "|"+value+"|") {
			return p, fmt.Errorf("invalid memory channel")
		}
	} else if key == "display-theme" {
		if value != "default" && value != "no-color" && value != "monochrome" && value != "high-contrast" {
			return p, fmt.Errorf("invalid display theme")
		}
	} else if def == "on" {
		if value != "on" && value != "off" {
			return p, fmt.Errorf("%s needs on|off", key)
		}
	} else {
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n > 3600 {
			return p, fmt.Errorf("%s needs a number from 1 to 3600", key)
		}
	}
	if key == "thermal-window" {
		n, _ := strconv.Atoi(value)
		if n > 60 {
			return p, fmt.Errorf("thermal-window must be from 1 to 60 seconds")
		}
	}
	old := Preference(p, key)
	copyValues := map[string]string{}
	for k, v := range p.Values {
		copyValues[k] = v
	}
	copyValues[key] = value
	next := p
	next.Values = copyValues
	next.Revision++
	trigger, _ := strconv.Atoi(Preference(next, "thermal-trigger"))
	release, _ := strconv.Atoi(Preference(next, "thermal-release"))
	if release >= trigger {
		return p, fmt.Errorf("thermal release must be below trigger")
	}
	var id [16]byte
	if _, err = rand.Read(id[:]); err != nil {
		return p, err
	}
	c := PreferenceChange{ID: hex.EncodeToString(id[:]), Revision: next.Revision, Key: key, Old: old, New: value, Actor: actor, At: time.Now().UTC()}
	next.Changes = append(append([]PreferenceChange{}, p.Changes...), c)
	if len(next.Changes) > 100 {
		next.Changes = next.Changes[len(next.Changes)-100:]
	}
	data, err := json.Marshal(next)
	if err != nil {
		return p, err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".preferences-*")
	if err != nil {
		return p, err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return p, err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return p, err
	}
	return next, nil
}

// UserPreference reads a default when no user record exists. Invalid records
// are reported by Settings; launch readers keep their established defaults.
func UserPreference(key string) string {
	path, err := PreferencesPath()
	if err != nil {
		v, _ := preferenceDefault(key)
		return v
	}
	p, err := ReadPreferences(path)
	if err != nil {
		v, _ := preferenceDefault(key)
		return v
	}
	return Preference(p, key)
}

func UserRows(p Preferences, getenv func(string) string) []Row {
	out := []Row{}
	for _, key := range []string{"update-check", "web-auto-open", "thermal-enabled", "thermal-trigger", "thermal-release", "thermal-window", "display-theme", "display-animation"} {
		raw := Preference(p, key)
		eff, source := raw, "user default"
		if _, ok := p.Values[key]; ok {
			source = "saved user preference"
		}
		if key == "update-check" && updateOptOut(getenv("MARSHAL_NO_UPDATE_CHECK")) {
			eff, source = "off", "launch environment"
		}
		if key == "web-auto-open" && getenv("MARSHAL_WEB_OPEN") == "0" {
			eff, source = "off", "launch environment"
		}
		if key == "display-animation" && getenv("MARSHAL_REDUCED_MOTION") != "" && getenv("MARSHAL_REDUCED_MOTION") != "0" {
			eff, source = "off", "launch environment"
		}
		if key == "display-theme" && getenv("NO_COLOR") != "" {
			eff, source = "no-color", "launch environment"
		}

		units := ""
		if key == "thermal-trigger" || key == "thermal-release" {
			units = "°C"
		}
		if key == "thermal-window" {
			units = "seconds"
		}
		out = append(out, Row{Key: key, Value: raw, Effective: eff, Next: raw, Source: source, Scope: "user", Units: units, Applies: "next launch", Help: "Launch opt-outs remain effective."})
	}
	return out
}

func updateOptOut(v string) bool {
	v = strings.TrimSpace(v)
	return v != "" && v != "0" && !strings.EqualFold(v, "false")
}

// ReadScopedPreference also accepts the earlier one-line file representation.
func ReadScopedPreference(path, key string) (Preferences, error) {
	p, err := ReadPreferences(path)
	if err == nil {
		return p, nil
	}
	raw, readErr := os.ReadFile(path)
	if readErr != nil {
		return p, readErr
	}
	value := strings.TrimSpace(string(raw))
	if strings.HasPrefix(value, "{") {
		return p, err
	}
	return Preferences{Values: map[string]string{key: value}, Changes: []PreferenceChange{}}, nil
}

// WriteScopedPreference replaces a legacy file with one atomic value/audit record.
func WriteScopedPreference(path, key, value, actor string) (Preferences, error) {
	p, err := ReadScopedPreference(path, key)
	if err != nil {
		return p, err
	}
	return applyPreferenceRecord(path, key, value, actor, p.Revision, p)
}
