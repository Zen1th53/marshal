// Package settingscatalog describes project settings and their application boundaries.
package settingscatalog

import (
	"fmt"
	"github.com/Zen1th53/marshal/internal/marshal"
	"reflect"
	"strconv"
	"strings"
)

type Descriptor struct {
	Key        string   `json:"key"`
	Label      string   `json:"label"`
	Kind       string   `json:"kind"`
	Units      string   `json:"units"`
	Values     []string `json:"values"`
	Default    string   `json:"default"`
	Editions   []string `json:"editions"`
	Scope      string   `json:"scope"`
	Applies    string   `json:"applies"`
	LockReason string   `json:"lockReason"`
	Confirm    bool     `json:"confirm"`
	Help       string   `json:"help"`
	get        func(marshal.Settings) string
	set        func(*marshal.Settings, string) error
}

func field[T ~string | ~int | ~int64](key, kind, units string, values []string, ptr func(*marshal.Settings) *T) Descriptor {
	d := Descriptor{Key: key, Label: key, Kind: kind, Units: units, Values: values, Editions: []string{"default", "governed"}, Scope: "project", Applies: "next run", Help: "Approved and running settings keep their snapshot."}
	d.get = func(s marshal.Settings) string { return fmt.Sprint(*ptr(&s)) }
	d.set = func(s *marshal.Settings, raw string) error {
		v := reflect.ValueOf(ptr(s)).Elem()
		if v.Kind() == reflect.String {
			v.SetString(raw)
		} else {
			n, err := strconv.ParseInt(raw, 10, v.Type().Bits())
			if err != nil || n < 0 {
				return fmt.Errorf("%s needs a non-negative number (%s)", key, units)
			}
			v.SetInt(n)
		}
		return nil
	}
	d.Default = d.get(marshal.DefaultSettings())
	return d
}

func Catalogue() []Descriptor {
	rows := []Descriptor{
		field("execution-rights", "enum", "", []string{"read-only", "small-tasks"}, func(s *marshal.Settings) *marshal.ExecutionRights { return &s.ExecutionRights }),
		field("acceptance-mode", "enum", "", []string{"marshal", "marshal-then-user", "user"}, func(s *marshal.Settings) *marshal.AcceptanceMode { return &s.AcceptanceMode }),
		field("control", "enum", "", []string{"free", "strict"}, func(s *marshal.Settings) *marshal.Control { return &s.Control }),
		field("rework-limit", "integer", "attempts", nil, func(s *marshal.Settings) *int { return &s.ReworkLimit }),
		field("ultra-concurrency", "integer", "workers", nil, func(s *marshal.Settings) *int { return &s.UltraConcurrency }),
		field("worker-stall-minutes", "integer", "minutes", nil, func(s *marshal.Settings) *int { return &s.WorkerStallMinutes }),
		field("task-tokens", "integer", "tokens", nil, func(s *marshal.Settings) *int64 { return &s.Budget.Tokens.Task }),
		field("plan-tokens", "integer", "tokens", nil, func(s *marshal.Settings) *int64 { return &s.Budget.Tokens.Plan }),
		field("task-money", "integer", "minor currency units", nil, func(s *marshal.Settings) *int64 { return &s.Budget.Money.Task }),
		field("plan-money", "integer", "minor currency units", nil, func(s *marshal.Settings) *int64 { return &s.Budget.Money.Plan }),
		field("task-wall-seconds", "integer", "seconds", nil, func(s *marshal.Settings) *int64 { return &s.Budget.WallTime.Task }),
		field("plan-wall-seconds", "integer", "seconds", nil, func(s *marshal.Settings) *int64 { return &s.Budget.WallTime.Plan }),
	}
	rows = append(rows, editionRows()...)
	for i := range rows {
		if rows[i].Key == "control" {
			rows[i].Default = string(marshal.DefaultSettings().EffectiveControl())
		}
		if strings.HasPrefix(rows[i].Key, "task-") || strings.HasPrefix(rows[i].Key, "plan-") {
			rows[i].Help += " Zero leaves this ceiling unlimited."
		}
		if rows[i].Key == "rework-limit" {
			rows[i].Help += " Zero allows no rework attempts."
		}
		configureEditionDescriptor(&rows[i])
	}
	return rows
}

func Canonical(key string) string {
	key = strings.ToLower(key)
	for _, p := range []string{"codex", "claude", "agy", "opencode", "antigravity"} {
		if key == p || key == p+"-concurrency" || key == p+"-limit" {
			if p == "antigravity" {
				p = "agy"
			}
			return p + "-limit"
		}
	}
	if key == "global-limit" || key == "global-concurrency" {
		return "global-cap"
	}
	return key
}

func Find(key string) (Descriptor, error) {
	key = Canonical(key)
	for _, d := range Catalogue() {
		if d.Key == key {
			return d, nil
		}
	}
	return Descriptor{}, fmt.Errorf("unknown setting %q", key)
}

func Get(s marshal.Settings, key string) (string, error) {
	d, err := Find(key)
	if err != nil {
		return "", err
	}
	return d.get(s), nil
}

// Apply returns a validated copy. Failure never changes the caller's value.
func Apply(s marshal.Settings, key, value string) (marshal.Settings, error) {
	key = Canonical(key)
	d, err := Find(key)
	if err != nil {
		return s, err
	}
	if d.LockReason != "" {
		return s, fmt.Errorf("%s: %s", key, d.LockReason)
	}
	next := s
	if key == "execution-rights" && value == "none" {
		value = "read-only"
	}
	if err = d.set(&next, value); err != nil {
		return s, err
	}
	if err = next.Validate(); err != nil {
		return s, err
	}
	return next, nil
}

func Validate(s marshal.Settings, key, value string) error {
	_, err := Apply(s, key, value)
	return err
}

// Effective separates legacy/unconfigured values from the values used by a new run.
func Effective(s marshal.Settings, key string) string {
	key = Canonical(key)
	if key == "execution-rights" && s.ExecutionRights == marshal.RightsNone {
		return "read-only"
	}
	if key == "control" {
		return string(s.EffectiveControl())
	}
	if v, ok := editionEffective(s, key); ok {
		return v
	}
	v, _ := Get(s, key)
	return v
}

type Row struct {
	Key          string `json:"key"`
	Value        string `json:"value"`
	Effective    string `json:"effective"`
	Next         string `json:"next"`
	Applies      string `json:"applies"`
	LockedReason string `json:"lockedReason"`
	Source       string `json:"source"`
	Help         string `json:"help"`
	Units        string `json:"units"`
	Scope        string `json:"scope"`
}

func Rows(s marshal.Settings, revision int64) []Row {
	out := []Row{}
	for _, d := range Catalogue() {
		source := "project"
		if revision == 0 {
			source = "catalogue default"
		}
		raw := d.get(s)
		effective := Effective(s, d.Key)
		if raw != effective {
			source = "runtime default"
		}
		if d.LockReason != "" {
			source = "edition policy"
		}
		out = append(out, Row{Key: d.Key, Value: raw, Effective: effective, Next: effective, Applies: d.Applies, LockedReason: d.LockReason, Source: source, Help: d.Help, Units: d.Units, Scope: d.Scope})
	}
	return out
}
