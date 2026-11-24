//go:build governed

package settingscatalog

import "github.com/Zen1th53/marshal/internal/marshal"

func editionRows() []Descriptor {
	rows := []Descriptor{}
	for _, key := range []string{"codex-limit", "claude-limit", "agy-limit", "opencode-limit", "global-cap", "failover-order"} {
		rows = append(rows, Descriptor{Key: key, Label: key, Kind: "policy", Default: "not available", Editions: []string{"default"}, Scope: "build", Applies: "this build", LockReason: "not configurable in this edition", Help: "Workers use the run concurrency limit.", get: func(marshal.Settings) string { return "not available" }})
	}
	return rows
}
func editionEffective(marshal.Settings, string) (string, bool) { return "", false }

func configureEditionDescriptor(d *Descriptor) {
	switch d.Key {
	case "worker-stall-minutes":
		d.LockReason = "not used in this edition"
		d.Applies = "this build"
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
}
