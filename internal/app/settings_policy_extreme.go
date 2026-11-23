//go:build !governed

package app

import (
	"fmt"
	"github.com/Zen1th53/marshal/internal/settingscatalog"
)

func editionSettingsPolicies() []settingscatalog.Row {
	rows := []settingscatalog.Row{}
	for _, p := range [][2]string{{"verification-timeout", verificationTimeout.String()}, {"infra-retry-limit", fmt.Sprint(infraRetryLimit)}, {"infra-failover-streak", fmt.Sprint(infraFailoverStreak)}, {"infra-cooldown", infraFailureCooldown.String()}, {"design-review", "on demand"}, {"architecture-conformance", "required with accepted contract"}, {"ultra-entitlement", "local"}, {"worker-memory-writes", "read-only"}} {
		rows = append(rows, settingscatalog.Row{Key: p[0], Value: p[1], Effective: p[1], Next: p[1], Scope: "build", Source: "edition policy", Applies: "this build", LockedReason: "fixed in this edition", Help: "About this build"})
	}
	return rows
}
