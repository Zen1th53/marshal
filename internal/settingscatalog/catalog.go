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
	Key          string            `json:"key"`
	Label        string            `json:"label"`
	Kind         string            `json:"kind"`
	Units        string            `json:"units"`
	Values       []string          `json:"values"`
	Default      string            `json:"default"`
	Editions     []string          `json:"editions"`
	Scope        string            `json:"scope"`
	Applies      string            `json:"applies"`
	LockReason   string            `json:"lockReason"`
	LockedValues map[string]string `json:"lockedValues,omitempty"`
	Confirm      bool              `json:"confirm"`
	Help         string            `json:"help"`
	get          func(marshal.Settings) string
	set          func(*marshal.Settings, string) error
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

func providerEnabledField(provider string) Descriptor {
	key := provider + "-enabled"
	d := Descriptor{
		Key:      key,
		Label:    key,
		Kind:     "enum",
		Values:   []string{"on", "off"},
		Default:  "on",
		Editions: []string{"default", "governed"},
		Scope:    "project",
		Applies:  "next admission",
		Help:     "Controls worker eligibility for this provider. Running workers finish; changes apply at the next admission.",
	}
	d.get = func(s marshal.Settings) string {
		if s.ProviderEnabled(provider) {
			return "on"
		}
		return "off"
	}
	d.set = func(s *marshal.Settings, raw string) error {
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "on":
			return s.SetProviderEnabled(provider, true)
		case "off":
			return s.SetProviderEnabled(provider, false)
		default:
			return fmt.Errorf("%s needs on|off", key)
		}
	}
	return d
}

func workerStallField() Descriptor {
	d := Descriptor{
		Key:      "worker-stall-minutes",
		Label:    "worker-stall-minutes",
		Kind:     "integer",
		Units:    "minutes",
		Editions: []string{"default", "governed"},
		Scope:    "project",
		Applies:  "next run",
		Help:     "Approved and running settings keep their snapshot.",
	}
	d.get = func(s marshal.Settings) string {
		if s.WorkerStallOff {
			return "0"
		}
		if s.WorkerStallMinutes == 0 {
			return ""
		}
		return strconv.Itoa(s.WorkerStallMinutes)
	}
	d.set = func(s *marshal.Settings, raw string) error {
		trimmed := strings.ToLower(strings.TrimSpace(raw))
		if trimmed == "" || trimmed == "default" {
			s.WorkerStallMinutes = 0
			s.WorkerStallOff = false
			return nil
		}
		if trimmed == "off" {
			s.WorkerStallMinutes = 0
			s.WorkerStallOff = true
			return nil
		}
		n, err := strconv.Atoi(trimmed)
		if err != nil || n < 0 {
			return fmt.Errorf("worker-stall-minutes needs a non-negative number (minutes) or off")
		}
		s.WorkerStallMinutes = n
		s.WorkerStallOff = n == 0
		return nil
	}
	d.Default = d.get(marshal.DefaultSettings())
	return d
}

func Catalogue() []Descriptor {
	rows := []Descriptor{
		field("final-provider", "enum", "", []string{"any", "codex", "claude", "opencode", "agy"}, func(s *marshal.Settings) *string { return &s.ReviewPolicy.FinalProvider }),
		field("execution-rights", "enum", "", []string{"read-only", "small-tasks"}, func(s *marshal.Settings) *marshal.ExecutionRights { return &s.ExecutionRights }),
		field("acceptance-mode", "enum", "", []string{"marshal", "marshal-then-user", "user"}, func(s *marshal.Settings) *marshal.AcceptanceMode { return &s.AcceptanceMode }),
		field("control", "enum", "", []string{"free", "strict"}, func(s *marshal.Settings) *marshal.Control { return &s.Control }),
		field("cross-review", "enum", "", []string{"required", "off"}, func(s *marshal.Settings) *marshal.CrossReviewPolicy { return &s.ReviewPolicy.CrossReview }),
		field("design-review", "enum", "", []string{"on-demand", "required", "off"}, func(s *marshal.Settings) *marshal.DesignReviewPolicy { return &s.ReviewPolicy.Design }),
		field("architecture-conformance", "enum", "", []string{"enforce", "warn", "off"}, func(s *marshal.Settings) *marshal.ConformancePolicy { return &s.ReviewPolicy.Conformance }),
		field("final-model", "enum", "", []string{"required", "off"}, func(s *marshal.Settings) *marshal.FinalModelPolicy { return &s.ReviewPolicy.FinalModel }),
		field("rework-limit", "integer", "attempts", nil, func(s *marshal.Settings) *int { return &s.ReworkLimit }),
		field("ultra-concurrency", "integer", "workers", nil, func(s *marshal.Settings) *int { return &s.UltraConcurrency }),
		workerStallField(),
		providerEnabledField("codex"),
		providerEnabledField("claude"),
		providerEnabledField("agy"),
		providerEnabledField("opencode"),
		field("task-tokens", "integer", "tokens", nil, func(s *marshal.Settings) *int64 { return &s.Budget.Tokens.Task }),
		field("plan-tokens", "integer", "tokens", nil, func(s *marshal.Settings) *int64 { return &s.Budget.Tokens.Plan }),
		field("task-money", "integer", "minor currency units", nil, func(s *marshal.Settings) *int64 { return &s.Budget.Money.Task }),
		field("plan-money", "integer", "minor currency units", nil, func(s *marshal.Settings) *int64 { return &s.Budget.Money.Plan }),
		field("task-wall-seconds", "integer", "seconds", nil, func(s *marshal.Settings) *int64 { return &s.Budget.WallTime.Task }),
		field("plan-wall-seconds", "integer", "seconds", nil, func(s *marshal.Settings) *int64 { return &s.Budget.WallTime.Plan }),
	}
	rows = append(rows, editionRows()...)
	for i := range rows {
		if rows[i].Key == "final-provider" {
			rows[i].Default = "any"
			rows[i].Help = "Final verifier family: any prefers a different implementing family; a pin still requires native identity, a fresh session and exact commit binding."
		}
		if rows[i].Key == "control" {
			rows[i].Default = string(marshal.DefaultSettings().EffectiveControl())
		}
		if rows[i].Key == "final-model" {
			rows[i].LockReason = "canonical completion requires the independent verifier"
			rows[i].Help = "Canonical completion requires the independent verifier; no settings edit alone authorizes delivery."
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
		if key == p+"-enabled" {
			if p == "antigravity" {
				p = "agy"
			}
			return p + "-enabled"
		}
	}
	if key == "global-limit" || key == "global-concurrency" {
		return "global-cap"
	}
	if key == "worker-stall" {
		return "worker-stall-minutes"
	}
	if key == "verification-timeout" {
		return "verification-timeout-seconds"
	}
	if key == "infra-cooldown" {
		return "infra-cooldown-seconds"
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
		if isReviewPolicyKey(key) {
			return s, fmt.Errorf("%s: %s: %w", key, d.LockReason, marshal.ErrReviewPolicyLocked)
		}
		return s, fmt.Errorf("%s: %s", key, d.LockReason)
	}
	if reason, locked := d.LockedValues[value]; locked {
		if isReviewPolicyKey(key) {
			return s, fmt.Errorf("%s=%s: %s: %w", key, value, reason, marshal.ErrReviewPolicyLocked)
		}
		return s, fmt.Errorf("%s=%s: %s", key, value, reason)
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
	switch key {
	case "cross-review":
		return string(s.ReviewPolicy.Effective().CrossReview)
	case "design-review":
		return string(s.ReviewPolicy.Effective().Design)
	case "architecture-conformance":
		return string(s.ReviewPolicy.Effective().Conformance)
	case "final-provider":
		return s.ReviewPolicy.Effective().FinalProvider
	case "final-model":
		return string(s.ReviewPolicy.Effective().FinalModel)
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

func isReviewPolicyKey(key string) bool {
	switch key {
	case "cross-review", "design-review", "architecture-conformance", "final-model", "final-provider":
		return true
	}
	return false
}

// Rows returns the project setting rows for display.
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
