//go:build !governed

package tui

import (
	"fmt"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/settingscatalog"
)

func applyExtremeSetting(s *marshal.Settings, args []string) (bool, string, error) {
	if len(args) == 0 {
		return false, "", nil
	}
	key := settingscatalog.Canonical(args[0])
	switch key {
	case "codex-limit", "claude-limit", "agy-limit", "opencode-limit", "global-cap":
		if len(args) != 2 {
			return true, "", fmt.Errorf("%s needs a number", key)
		}
		next, err := settingscatalog.Apply(*s, key, args[1])
		if err != nil {
			return true, "", err
		}
		*s = next
		return true, "", nil
	}
	return false, "", nil
}

func formatExtremeSettings(s marshal.Settings) string {
	limits, global := marshal.EffectiveWorkerLimits(s)
	codex, claude, agy, opencode := limits["codex"], limits["claude"], limits["agy"], limits["opencode"]

	return fmt.Sprintf("codex-limit %d\nclaude-limit %d\nagy-limit %d\nopencode-limit %d\nglobal-cap %d",
		codex, claude, agy, opencode, global)
}

// effectiveGlobalCap is the global lane cap a run with these settings
// dispatches under, as the extreme dispatcher computes it.
func effectiveGlobalCap(s marshal.Settings) (int, bool) {
	_, cap := marshal.EffectiveWorkerLimits(s)
	return cap, true
}

// projectGlobalCap is the stored global-cap setting; 0 is the default.
func projectGlobalCap(s marshal.Settings) int { return s.GlobalCap }
