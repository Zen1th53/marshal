package usage

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"time"
)

// claudeMaxBytes is the largest .claude.json read; a larger file is
// malformed (its size is checked before it is read).
const claudeMaxBytes = 16 << 20

// ClaudeConfigFile is where Claude Code writes .claude.json. CLAUDE_CONFIG_DIR
// overrides the home directory, but only when absolute: a relative override is
// unusable (""), and the home file is never used as a fallback for a set
// CLAUDE_CONFIG_DIR, which may be another account.
func ClaudeConfigFile() string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		if !filepath.IsAbs(dir) {
			return ""
		}
		return filepath.Join(dir, ".claude.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude.json")
}

// readClaude reads the usage figures Claude Code last wrote to path. Only
// cachedUsageUtilization is read; no account field is ever touched.
func readClaude(now time.Time, path string) Quota {
	q := Quota{Provider: "claude"}
	if path == "" {
		q.Reason = ReasonNoFile
		return q
	}
	info, err := os.Stat(path)
	if err != nil {
		q.Reason = ReasonNoFile
		return q
	}
	if info.Size() > claudeMaxBytes {
		q.Reason = ReasonMalformed
		return q
	}
	data, err := os.ReadFile(path)
	if err != nil {
		q.Reason = ReasonNoFile
		return q
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var top map[string]any
	if err := dec.Decode(&top); err != nil {
		q.Reason = ReasonMalformed
		return q
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		q.Reason = ReasonMalformed
		return q
	}

	rawCached, present := top["cachedUsageUtilization"]
	if !present || rawCached == nil {
		q.Reason = ReasonNotReported
		return q
	}
	cached, ok := rawCached.(map[string]any)
	if !ok {
		q.Reason = ReasonMalformed
		return q
	}

	ms, ok, why := number(cached, "fetchedAtMs")
	if !ok {
		q.Reason = why
		return q
	}
	if ms != math.Trunc(ms) || ms <= 0 {
		q.Reason = ReasonMalformed
		return q
	}
	asOf := time.UnixMilli(int64(ms))
	age := now.Sub(asOf)
	if age < 0 {
		age = 0 // clock skew: a report from the future is treated as current.
	}
	if age >= staleAfter {
		q.Reason = ReasonStale
		return q
	}

	rawUtil, present := cached["utilization"]
	if !present || rawUtil == nil {
		q.Reason = ReasonNotReported
		return q
	}
	util, ok := rawUtil.(map[string]any)
	if !ok {
		q.Reason = ReasonMalformed
		return q
	}

	firstReason := ""
	for _, kl := range []struct{ key, label string }{{"five_hour", "5h"}, {"seven_day", "wk"}} {
		raw, present := util[kl.key]
		if !present || raw == nil {
			continue
		}
		win, why := claudeWindow(now, kl.label, raw)
		q.Windows = append(q.Windows, win)
		if win.Known() {
			q.Known = true
		} else if firstReason == "" {
			firstReason = why
		}
	}
	switch {
	case len(q.Windows) == 0:
		q.Reason = ReasonNotReported
	case !q.Known:
		q.Reason = firstReason
	}
	if q.Known {
		q.AsOf = asOf
		q.Stale = age >= FreshFor
	}
	return q
}

// claudeWindow turns one utilization entry into a Window, unknown with a
// reason unless every field is present and in range.
func claudeWindow(now time.Time, label string, raw any) (Window, string) {
	win := Window{Label: label}
	obj, ok := raw.(map[string]any)
	if !ok {
		return win, ReasonMalformed
	}
	used, ok, why := number(obj, "utilization")
	if !ok {
		return win, why
	}
	if used < 0 || used > 100 {
		return win, ReasonOutOfRange
	}
	r, present := obj["resets_at"]
	if !present || r == nil {
		return win, ReasonMissing
	}
	s, ok := r.(string)
	if !ok {
		return win, ReasonMalformed
	}
	resets, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return win, ReasonMalformed
	}
	if !resets.After(now) {
		return win, ReasonReset
	}
	win.LeftPercent, win.ResetsAt = 100-used, resets
	return win, ""
}
