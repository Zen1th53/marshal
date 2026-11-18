package usage

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"time"
)

// Claude Code no longer writes cachedUsageUtilization to .claude.json. It
// reports its limits in its stream-json output instead, as a
// rate_limit_event whose rate_limit_info.unifiedWindows carries each
// window's utilization and reset time. MARSHAL records the latest such event
// it sees from any Claude it runs, and readClaude falls back to it.

// ClaudeRateLimitCache is where the latest Claude rate_limit_event is kept.
func ClaudeRateLimitCache() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "marshal", "claude-rate-limits.json")
}

type claudeRateLimitRecord struct {
	RecordedAt time.Time                       `json:"recorded_at"`
	Windows    map[string]claudeRateLimitEntry `json:"windows"`
}

type claudeRateLimitEntry struct {
	Utilization float64 `json:"utilization"`
	ResetsAt    int64   `json:"resetsAt"`
	Status      string  `json:"status"`
}

// RecordClaudeRateLimit keeps line when it is a Claude stream-json
// rate_limit_event. Any other line costs one byte search.
func RecordClaudeRateLimit(line []byte) {
	if !bytes.Contains(line, []byte(`"rate_limit_event"`)) {
		return
	}
	recordClaudeRateLimitAt(line, time.Now(), ClaudeRateLimitCache())
}

func recordClaudeRateLimitAt(line []byte, now time.Time, path string) {
	if path == "" {
		return
	}
	var event struct {
		Type string `json:"type"`
		Info struct {
			Windows map[string]claudeRateLimitEntry `json:"unifiedWindows"`
		} `json:"rate_limit_info"`
	}
	if json.Unmarshal(bytes.TrimSpace(line), &event) != nil || event.Type != "rate_limit_event" || len(event.Info.Windows) == 0 {
		return
	}
	data, err := json.Marshal(claudeRateLimitRecord{RecordedAt: now.UTC(), Windows: event.Info.Windows})
	if err != nil {
		return
	}
	if os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return
	}
	temp := path + ".tmp"
	if os.WriteFile(temp, data, 0o600) == nil {
		_ = os.Rename(temp, path)
	}
}

// readClaudeEvents is the quota from the recorded rate_limit_event, with
// ok false when there is none.
func readClaudeEvents(now time.Time, path string) (Quota, bool) {
	q := Quota{Provider: "claude"}
	if path == "" {
		return q, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return q, false
	}
	var rec claudeRateLimitRecord
	if json.Unmarshal(data, &rec) != nil || rec.RecordedAt.IsZero() {
		return q, false
	}
	for _, kl := range []struct{ key, label string }{{"five_hour", "5h"}, {"seven_day", "wk"}} {
		e, ok := rec.Windows[kl.key]
		if !ok || e.ResetsAt <= 0 || math.IsNaN(e.Utilization) || e.Utilization < 0 || e.Utilization > 1.5 {
			continue
		}
		resets := time.Unix(e.ResetsAt, 0)
		if !resets.After(now) {
			// The window reset since it was read: its figure no longer holds.
			q.Windows = append(q.Windows, Window{Label: kl.label})
			continue
		}
		left := 100 * (1 - min(1, e.Utilization))
		q.Windows = append(q.Windows, Window{Label: kl.label, LeftPercent: left, ResetsAt: resets})
	}
	for _, w := range q.Windows {
		if w.Known() {
			q.Known = true
		}
	}
	if !q.Known {
		return q, false
	}
	q.AsOf = rec.RecordedAt
	q.Stale = now.Sub(rec.RecordedAt) >= FreshFor
	return q, true
}
