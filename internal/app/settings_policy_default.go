//go:build governed

package app

import "github.com/Zen1th53/marshal/internal/settingscatalog"

func editionSettingsPolicies() []settingscatalog.Row {
	rows := []settingscatalog.Row{}
	for _, p := range [][2]string{{"design-review", "not available"}, {"architecture-conformance", "not available"}, {"ultra-entitlement", "signed cloud lease"}, {"worker-memory-writes", "candidates require review"}} {
		rows = append(rows, settingscatalog.Row{Key: p[0], Value: p[1], Effective: p[1], Next: p[1], Scope: "build", Source: "edition policy", Applies: "this build", LockedReason: "fixed in this edition", Help: "About this build"})
	}
	return rows
}
