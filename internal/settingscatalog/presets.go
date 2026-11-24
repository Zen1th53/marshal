package settingscatalog

import (
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// PresetDiff describes one setting change proposed by a preset.
type PresetDiff struct {
	Key string `json:"key"`
	Old string `json:"old"`
	New string `json:"new"`
}

// PresetPreview reports the validated changes a preset would apply.
type PresetPreview struct {
	Preset  string       `json:"preset"`
	Changes []PresetDiff `json:"changes"`
}

type presetEntry struct {
	key   string
	value string
}

func presetEntries(name string) ([]presetEntry, string, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "fast":
		return []presetEntry{
			{key: "control", value: string(marshal.ControlFree)},
			{key: "cross-review", value: string(marshal.CrossReviewOff)},
			{key: "design-review", value: string(marshal.DesignReviewOff)},
			{key: "architecture-conformance", value: string(marshal.ConformanceWarn)},
		}, "fast", nil
	case "balanced":
		return []presetEntry{
			{key: "control", value: string(marshal.ControlFree)},
			{key: "cross-review", value: string(marshal.CrossReviewRequired)},
			{key: "design-review", value: string(marshal.DesignReviewOnDemand)},
			{key: "architecture-conformance", value: string(marshal.ConformanceEnforce)},
		}, "balanced", nil
	case "strict":
		return []presetEntry{
			{key: "control", value: string(marshal.ControlStrict)},
			{key: "cross-review", value: string(marshal.CrossReviewRequired)},
			{key: "design-review", value: string(marshal.DesignReviewRequired)},
			{key: "architecture-conformance", value: string(marshal.ConformanceEnforce)},
		}, "strict", nil
	default:
		return nil, "", fmt.Errorf("unknown preset %q", name)
	}
}

// PreviewPreset validates a named preset against current settings and edition
// locks without modifying the caller's value.
func PreviewPreset(current marshal.Settings, name string) (PresetPreview, error) {
	_, preview, err := ApplyPreset(current, name)
	return preview, err
}

// ApplyPreset returns a validated settings copy with the preset applied and
// the exact diff preview. Locked or invalid presets fail before any change.
func ApplyPreset(current marshal.Settings, name string) (marshal.Settings, PresetPreview, error) {
	entries, canonical, err := presetEntries(name)
	if err != nil {
		return current, PresetPreview{}, err
	}
	if err := current.Validate(); err != nil {
		return current, PresetPreview{}, err
	}
	next := current
	preview := PresetPreview{Preset: canonical, Changes: []PresetDiff{}}
	for _, entry := range entries {
		oldVal := Effective(next, entry.key)
		if oldVal == entry.value {
			continue
		}
		updated, err := Apply(next, entry.key, entry.value)
		if err != nil {
			return current, PresetPreview{}, fmt.Errorf("preset %s cannot change %s: %w", canonical, entry.key, err)
		}
		next = updated
		preview.Changes = append(preview.Changes, PresetDiff{Key: entry.key, Old: oldVal, New: entry.value})
	}
	if err := next.Validate(); err != nil {
		return current, PresetPreview{}, err
	}
	return next, preview, nil
}

// PresetFor identifies whether settings match a built-in preset or custom.
func PresetFor(s marshal.Settings) string {
	for _, name := range []string{"balanced", "strict", "fast"} {
		entries, canonical, _ := presetEntries(name)
		matched := true
		for _, entry := range entries {
			if Effective(s, entry.key) != entry.value {
				matched = false
				break
			}
		}
		if matched {
			return canonical
		}
	}
	return "custom"
}
