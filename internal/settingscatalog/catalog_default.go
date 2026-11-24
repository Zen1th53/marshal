//go:build governed

package settingscatalog

import "github.com/Zen1th53/marshal/internal/marshal"

func editionRows() []Descriptor {
	rows := []Descriptor{}
	for _, key := range []string{"codex-limit", "claude-limit", "agy-limit", "opencode-limit", "global-cap", "failover-order"} {
		rows = append(rows, Descriptor{Key: key, Label: key, Kind: "policy", Default: "not available", Editions: []string{"default"}, Scope: "build", Applies: "this build", LockReason: "not configurable in this edition", Help: "Workers use the run concurrency limit.", get: func(marshal.Settings) string { return "not available" }})
	}
	rows = append(rows,
		Descriptor{Key: "infra-retry-limit", Label: "infra-retry-limit", Kind: "policy", Units: "attempts", Default: "0", Editions: []string{"default"}, Scope: "build", Applies: "this build", LockReason: "not available in this edition", Help: "Infrastructure retry is not available in this edition.", get: func(marshal.Settings) string { return "0" }},
		Descriptor{Key: "infra-failover-streak", Label: "infra-failover-streak", Kind: "policy", Default: "not available", Editions: []string{"default"}, Scope: "build", Applies: "this build", LockReason: "not available in this edition", Help: "Automatic provider failover is not available in this edition.", get: func(marshal.Settings) string { return "not available" }},
		Descriptor{Key: "infra-cooldown-seconds", Label: "infra-cooldown-seconds", Kind: "policy", Default: "not available", Editions: []string{"default"}, Scope: "build", Applies: "this build", LockReason: "not available in this edition", Help: "Automatic provider failover is not available in this edition.", get: func(marshal.Settings) string { return "not available" }},
		Descriptor{Key: "verification-timeout-seconds", Label: "verification-timeout-seconds", Kind: "policy", Units: "seconds", Default: "900", Editions: []string{"default", "governed"}, Scope: "build", Applies: "this build", LockReason: "fixed at 15 minutes in this edition", Help: "Repository verification stays fixed at 15 minutes (900 seconds) in this edition.", get: func(marshal.Settings) string { return "900" }},
	)
	return rows
}

func editionEffective(marshal.Settings, string) (string, bool) { return "", false }

func configureEditionDescriptor(d *Descriptor) {
	switch d.Key {
	case "ultra-concurrency":
		d.Help = "Maximum number of workers at once while ULTRA execution is on (otherwise one at a time). Tasks of an approved governed plan still dispatch one at a time."
	case "worker-stall-minutes":
		d.LockReason = "not used in this edition"
		d.Applies = "this build"
		d.Scope = "build"
	case "cross-review", "architecture-conformance", "final-model":
		d.LockReason = "review policy is locked in this edition"
		d.Applies = "this build"
	case "design-review":
		d.Values = []string{string(marshal.DesignReviewOnDemand), string(marshal.DesignReviewRequired)}
		d.LockedValues = map[string]string{
			string(marshal.DesignReviewOff): "weakening review policy is locked in this edition",
		}
		d.Help = "Controls pre-approval independent design critique in a fresh session bound to the draft digest. Disabling design review (off) is locked in this edition."
	}
	if d.Key == "opencode-enabled" {
		d.LockReason = "not available in this edition"
		d.Applies = "this build"
		d.Scope = "build"
		d.Default = "not available"
		d.get = func(marshal.Settings) string { return "not available" }
	}
}

// EditionLabel names the build this catalogue describes.
const EditionLabel = "governed"
