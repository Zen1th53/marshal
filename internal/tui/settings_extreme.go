//go:build !governed

package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Zen1th53/marshal/internal/marshal"
)

func applyExtremeSetting(s *marshal.Settings, args []string) (bool, string, error) {
	if len(args) == 0 {
		return false, "", nil
	}
	key := strings.ToLower(args[0])
	if len(args) < 2 {
		switch key {
		case "codex", "codex-limit", "codex-concurrency",
			"claude", "claude-limit", "claude-concurrency",
			"agy", "agy-limit", "agy-concurrency", "antigravity", "antigravity-limit", "antigravity-concurrency",
			"opencode", "opencode-limit", "opencode-concurrency",
			"global-cap", "global-limit", "global-concurrency":
			return true, "", fmt.Errorf("%s needs a number", args[0])
		}
		return false, "", nil
	}

	n, err := strconv.Atoi(args[1])
	if err != nil || n < 0 {
		switch key {
		case "codex", "codex-limit", "codex-concurrency",
			"claude", "claude-limit", "claude-concurrency",
			"agy", "agy-limit", "agy-concurrency", "antigravity", "antigravity-limit", "antigravity-concurrency",
			"opencode", "opencode-limit", "opencode-concurrency",
			"global-cap", "global-limit", "global-concurrency":
			return true, "", fmt.Errorf("%s needs a non-negative number", args[0])
		}
		return false, "", nil
	}

	switch key {
	case "codex", "codex-limit", "codex-concurrency":
		s.ProviderLimits.Codex = n
		return true, "", nil
	case "claude", "claude-limit", "claude-concurrency":
		s.ProviderLimits.Claude = n
		return true, "", nil
	case "agy", "agy-limit", "agy-concurrency", "antigravity", "antigravity-limit", "antigravity-concurrency":
		s.ProviderLimits.Agy = n
		return true, "", nil
	case "opencode", "opencode-limit", "opencode-concurrency":
		s.ProviderLimits.OpenCode = n
		return true, "", nil
	case "global-cap", "global-limit", "global-concurrency":
		s.GlobalCap = n
		return true, "", nil
	}
	return false, "", nil
}

func formatExtremeSettings(s marshal.Settings) string {
	codex := 4
	claude := 2
	agy := 4
	opencode := 4
	global := 12

	if s.GlobalCap > 0 {
		global = s.GlobalCap
	}
	if s.ProviderLimits.Codex > 0 {
		codex = s.ProviderLimits.Codex
	}
	if s.ProviderLimits.Claude > 0 {
		claude = s.ProviderLimits.Claude
	}
	if s.ProviderLimits.Agy > 0 {
		agy = s.ProviderLimits.Agy
	}
	if s.ProviderLimits.OpenCode > 0 {
		opencode = s.ProviderLimits.OpenCode
	}

	return fmt.Sprintf("codex-limit %d\nclaude-limit %d\nagy-limit %d\nopencode-limit %d\nglobal-cap %d",
		codex, claude, agy, opencode, global)
}

// effectiveGlobalCap is the global lane cap a run with these settings
// dispatches under, as the extreme dispatcher computes it.
func effectiveGlobalCap(s marshal.Settings) (int, bool) {
	if s.GlobalCap > 0 {
		return s.GlobalCap, true
	}
	return 12, true
}

// projectGlobalCap is the stored global-cap setting; 0 is the default.
func projectGlobalCap(s marshal.Settings) int { return s.GlobalCap }
