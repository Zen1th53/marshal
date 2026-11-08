package app

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/marshal/driver"
)

var (
	rxRetryAfter = regexp.MustCompile(`(?i)retry[- ]after[:\s]+(\d+)(?:s)?\b`)
	rxResetsIn   = regexp.MustCompile(`(?i)(?:resets?\s+in|retry[- ]after)[:\s]+(\d+)\s*(seconds?|secs?|s|minutes?|mins?|m|hours?|hrs?|h|days?|d)\b`)
	rxResetsAt   = regexp.MustCompile(`(?i)resets?\s+at\s+([0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]+)?(?:Z|[+-][0-9]{2}:[0-9]{2}))`)
)

const defaultCooldown = 30 * time.Minute

// classifyProviderLimit inspects worker execution error and output to determine
// if the run ended on a provider usage or rate limit, extracting the reason and cooldown.
func classifyProviderLimit(err error, handin marshal.HandIn, h *driver.Handle) (bool, string, time.Duration) {
	return classifyProviderLimitAt(err, handin, h, time.Now())
}

func classifyProviderLimitAt(err error, handin marshal.HandIn, h *driver.Handle, now time.Time) (bool, string, time.Duration) {
	var sb strings.Builder
	failed := err != nil
	if err != nil {
		sb.WriteString(err.Error())
		sb.WriteString("\n")
	}
	if h != nil {
		obs, runErr, _ := h.Outcome()
		failed = failed || runErr != nil || obs.ExitCode != 0
		if runErr != nil {
			sb.WriteString(runErr.Error())
			sb.WriteString("\n")
		}
		if obs.Output != "" {
			sb.WriteString(obs.Output)
			sb.WriteString("\n")
		}
	}
	for _, rec := range handin.RuntimeObserved {
		if !strings.HasPrefix(rec.Command, "worker ") && rec.Command != "worker" {
			continue
		}
		failed = failed || rec.ExitCode != 0
		if rec.Output != "" {
			sb.WriteString(rec.Output)
			sb.WriteString("\n")
		}
	}

	fullText := sb.String()
	lower := strings.ToLower(fullText)
	if failed && providerLimitFallback && strings.Contains(lower, "worker stalled: no output or worktree change for ") {
		for _, line := range strings.Split(fullText, "\n") {
			if i := strings.Index(strings.ToLower(line), "worker stalled: no output or worktree change for "); i >= 0 {
				return true, strings.TrimSpace(line[i:]), 0
			}
		}
	}

	// An agy sign-in expiry fails over like a limit, for its own cooldown.
	if failed && strings.Contains(lower, agySigninExpiryReason) {
		return true, agySigninExpiryReason, agySigninExpiryCooldown
	}

	// Only real limit text counts: informational JSON events such as a
	// rate_limit_event with status "allowed" are dropped first.
	limitText := providerLimitText(fullText)
	isLimit := lineNamesLimit(strings.ToLower(limitText))

	if !isLimit || !failed {
		return false, "", 0
	}

	reason := extractLimitReason(limitText)
	cooldown := parseCooldown(limitText, now)
	return true, reason, cooldown
}

// limitMarkers name a provider rate or usage limit in lowercased output;
// editionLimitMarkers adds edition-specific wording.
var limitMarkers = []string{"usage limit", "usage-limit", "rate limit", "rate-limit", "rate_limit", "quota"}

func lineNamesLimit(lower string) bool {
	for _, m := range append(limitMarkers, editionLimitMarkers...) {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}

func extractLimitReason(text string) string {
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if lineNamesLimit(lower) {
			if len(trimmed) > 120 {
				return trimmed[:120]
			}
			return trimmed
		}
	}
	return "provider rate or usage limit exceeded"
}

func parseCooldown(text string, now time.Time) time.Duration {
	d, _ := parseCooldownFound(text, now)
	return d
}

// parseCooldownFound reads a provider's reset time from its message:
// "Resets in 103h", "Retry-After: 120", "resets at <RFC3339>", "reset at
// 5:03 AM" (next such local clock time) and "today's free messages" (next
// local midnight). It reports false and the default when none is named.
func parseCooldownFound(text string, now time.Time) (time.Duration, bool) {
	d, ok := parseCooldownText(text, now)
	if !ok {
		return defaultCooldown, false
	}
	return d, true
}

func parseCooldownText(text string, now time.Time) (time.Duration, bool) {
	// The private edition also reads a compound reset such as agy's
	// "Resets in 100h11m27s"; the public edition keeps its parsing.
	if providerLimitFallback {
		if d, ok := parseCompoundResetsIn(text); ok {
			return d, true
		}
	}
	if d, ok := parseClockReset(text, now); ok {
		return d, true
	}
	d := parseCooldownLegacy(text, now)
	if d != defaultCooldown || rxResetsIn.MatchString(text) || rxRetryAfter.MatchString(text) {
		return d, true
	}
	if rxToday.MatchString(text) {
		y, m, day := now.Date()
		return time.Date(y, m, day+1, 0, 0, 0, 0, now.Location()).Sub(now), true
	}
	return defaultCooldown, false
}

// rxResetsInCompound matches a reset written as a Go-style duration with at
// least two units, e.g. "Resets in 100h11m27s" or "resets in 3m20s".
var rxResetsInCompound = regexp.MustCompile(`(?i)resets?\s+in[:\s]+((?:\d+h)?(?:\d+m)?(?:\d+s)?)\b`)

func parseCompoundResetsIn(text string) (time.Duration, bool) {
	m := rxResetsInCompound.FindStringSubmatch(text)
	if len(m) < 2 || m[1] == "" {
		return 0, false
	}
	units := 0
	for _, unit := range []string{"h", "m", "s"} {
		if strings.Contains(m[1], unit) {
			units++
		}
	}
	if units < 2 {
		return 0, false
	}
	d, err := time.ParseDuration(m[1])
	if err != nil || d <= 0 {
		return 0, false
	}
	return d, true
}

var (
	rxClockReset = regexp.MustCompile(`(?i)resets?\s+(?:at\s+)?(\d{1,2}):(\d{2})\s*(am|pm)?\b`)
	rxToday      = regexp.MustCompile(`(?i)today'?s\s+(?:free\s+)?(?:messages|quota|limit)`)
)

func parseClockReset(text string, now time.Time) (time.Duration, bool) {
	m := rxClockReset.FindStringSubmatch(text)
	if len(m) < 4 {
		return 0, false
	}
	hour, _ := strconv.Atoi(m[1])
	minute, _ := strconv.Atoi(m[2])
	switch strings.ToLower(m[3]) {
	case "pm":
		if hour < 12 {
			hour += 12
		}
	case "am":
		if hour == 12 {
			hour = 0
		}
	}
	if hour > 23 || minute > 59 {
		return 0, false
	}
	y, mo, d := now.Date()
	at := time.Date(y, mo, d, hour, minute, 0, 0, now.Location())
	if !at.After(now) {
		at = at.AddDate(0, 0, 1)
	}
	return at.Sub(now), true
}

func parseCooldownLegacy(text string, now time.Time) time.Duration {
	if m := rxResetsIn.FindStringSubmatch(text); len(m) > 2 {
		if val, err := strconv.Atoi(m[1]); err == nil && val > 0 {
			switch strings.ToLower(m[2])[:1] {
			case "s":
				return safeCooldown(val, time.Second)
			case "m":
				return safeCooldown(val, time.Minute)
			case "h":
				return safeCooldown(val, time.Hour)
			case "d":
				return safeCooldown(val, 24*time.Hour)
			}
		}
	}
	if m := rxRetryAfter.FindStringSubmatch(text); len(m) > 1 {
		if secs, err := strconv.Atoi(m[1]); err == nil && secs > 0 {
			return safeCooldown(secs, time.Second)
		}
	}
	if m := rxResetsAt.FindStringSubmatch(text); len(m) > 1 {
		if t, err := time.Parse(time.RFC3339, m[1]); err == nil {
			if t.After(now) {
				return t.Sub(now)
			}
		}
	}
	return defaultCooldown
}

func safeCooldown(n int, unit time.Duration) time.Duration {
	if int64(n) > math.MaxInt64/int64(unit) {
		return defaultCooldown
	}
	return time.Duration(n) * unit
}
