package usage

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	// tailBytes is how much of the end of a rollout file is read.
	tailBytes = 256 << 10
	// staleAfter is how old a rollout file may be before its report is
	// no longer trusted.
	staleAfter = 24 * time.Hour
	// dayFolders is how many newest non-empty day folders are searched for
	// the newest rollout, so a session begun the day before still counts.
	dayFolders = 2
)

// Reasons a Codex reading is unknown.
const (
	ReasonNoFile      = "no file"
	ReasonStale       = "stale"
	ReasonNotReported = "not reported"
	ReasonMalformed   = "malformed"
	ReasonReset       = "reset since last report"
	ReasonOutOfRange  = "out of range"
	ReasonMissing     = "missing field"
)

// readCodex reads the rate limits Codex last reported in its newest rollout
// file under dir (dir/YYYY/MM/DD/rollout-*.jsonl). A session that has not
// reported yet (a chat just started, before its first turn) holds no report,
// so the newest file that does is read: the last report Codex wrote.
func readCodex(now time.Time, dir string) Quota {
	q := Quota{Provider: "codex"}
	files := rollouts(dir)
	if len(files) == 0 {
		q.Reason = ReasonNoFile
		return q
	}
	var line []byte
	var mod time.Time
	var from string
	readable := false
	for _, f := range files {
		if now.Sub(f.mod) >= staleAfter {
			if !readable {
				q.Reason = ReasonStale
				return q
			}
			break
		}
		l, err := lastRateLimitLine(f.path)
		if err != nil {
			continue
		}
		readable = true
		if l != nil {
			line, mod, from = l, f.mod, f.path
			break
		}
	}
	if !readable {
		q.Reason = ReasonNoFile
		return q
	}
	if line == nil {
		q.Reason = ReasonNotReported
		return q
	}
	limits, reason := decodeRateLimits(line)
	if reason != "" {
		q.Reason = reason
		return q
	}
	firstReason := ""
	for _, key := range []string{"primary", "secondary"} {
		raw, present := limits[key]
		if !present || raw == nil {
			continue
		}
		win, why := codexWindow(now, key, raw)
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
		q.AsOf = mod
		q.Stale = now.Sub(mod) >= FreshFor
		q.Model = lastModel(from)
	}
	return q
}

// lastModel is the model of the newest turn_context in the tail of the
// rollout at path, or "" when the tail names none. Only that one field is
// decoded, and only a plain model id is kept.
func lastModel(path string) string {
	line, err := lastLineWith(path, `"turn_context"`)
	if err != nil || line == nil {
		return ""
	}
	var rec struct {
		Type    string `json:"type"`
		Payload struct {
			Model string `json:"model"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &rec) != nil || rec.Type != "turn_context" {
		return ""
	}
	m := rec.Payload.Model
	if len(m) == 0 || len(m) > 64 {
		return ""
	}
	for _, c := range m {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return ""
		}
	}
	return m
}

// codexWindow turns one rate_limits entry into a Window, unknown with a
// reason unless every field is present and in range.
func codexWindow(now time.Time, key string, raw any) (Window, string) {
	win := Window{Label: key}
	obj, ok := raw.(map[string]any)
	if !ok {
		return win, ReasonMalformed
	}
	if m, present := obj["window_minutes"]; present {
		n, ok := m.(json.Number)
		if !ok {
			return win, ReasonMalformed
		}
		minutes, err := n.Int64()
		if err != nil || minutes <= 0 {
			return win, ReasonMalformed
		}
		win.Label = windowLabel(minutes)
	} else {
		return win, ReasonMissing
	}
	used, ok, why := number(obj, "used_percent")
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
	n, isNum := r.(json.Number)
	if !isNum {
		return win, ReasonMalformed
	}
	secs, err := n.Int64()
	if err != nil || secs <= 0 {
		return win, ReasonMalformed
	}
	resets := time.Unix(secs, 0)
	if !resets.After(now) {
		return win, ReasonReset
	}
	win.LeftPercent, win.ResetsAt = 100-used, resets
	return win, ""
}

func number(obj map[string]any, key string) (float64, bool, string) {
	v, present := obj[key]
	if !present || v == nil {
		return 0, false, ReasonMissing
	}
	n, ok := v.(json.Number)
	if !ok {
		return 0, false, ReasonMalformed
	}
	f, err := n.Float64()
	if err != nil {
		return 0, false, ReasonMalformed
	}
	return f, true, ""
}

// windowLabel names a window by its length: 5h, wk or <n>m.
func windowLabel(minutes int64) string {
	switch minutes {
	case 300:
		return "5h"
	case 10080:
		return "wk"
	}
	return strconv.FormatInt(minutes, 10) + "m"
}

// decodeRateLimits decodes one JSONL line strictly and finds its
// rate_limits object.
func decodeRateLimits(line []byte) (map[string]any, string) {
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, ReasonMalformed
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, ReasonMalformed
	}
	raw, found := findKey(v, "rate_limits")
	if !found {
		return nil, ReasonMalformed
	}
	if raw == nil {
		return nil, ReasonNotReported
	}
	limits, ok := raw.(map[string]any)
	if !ok {
		return nil, ReasonMalformed
	}
	return limits, ""
}

// findKey is the first value under key, depth first, in object key order.
func findKey(v any, key string) (any, bool) {
	switch t := v.(type) {
	case map[string]any:
		if val, ok := t[key]; ok {
			return val, true
		}
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if val, ok := findKey(t[k], key); ok {
				return val, true
			}
		}
	case []any:
		for _, e := range t {
			if val, ok := findKey(e, key); ok {
				return val, true
			}
		}
	}
	return nil, false
}

// lastRateLimitLine is the last complete line in the file's last tailBytes
// that mentions rate_limits, or nil when there is none. A first line cut by
// the tail window and a last line without its newline are skipped.
func lastRateLimitLine(path string) ([]byte, error) {
	return lastLineWith(path, `"rate_limits"`)
}

// lastLineWith is the last complete line in the tail of path that contains
// marker, or nil when none does.
func lastLineWith(path, marker string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	off := int64(0)
	if info.Size() > tailBytes {
		off = info.Size() - tailBytes
	}
	buf := make([]byte, info.Size()-off)
	n, err := f.ReadAt(buf, off)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	buf = buf[:n]
	if off > 0 {
		i := bytes.IndexByte(buf, '\n')
		if i < 0 {
			return nil, nil
		}
		buf = buf[i+1:]
	}
	end := bytes.LastIndexByte(buf, '\n')
	if end < 0 {
		return nil, nil
	}
	buf = buf[:end]
	for len(buf) > 0 {
		start := bytes.LastIndexByte(buf, '\n') + 1
		line := buf[start:]
		if bytes.Contains(line, []byte(marker)) {
			return bytes.TrimRight(line, "\r"), nil
		}
		if start == 0 {
			break
		}
		buf = buf[:start-1]
	}
	return nil, nil
}

// rolloutFile is one rollout file and its modification time.
type rolloutFile struct {
	path string
	mod  time.Time
}

// maxRollouts is how many of the newest rollouts are searched for a report.
const maxRollouts = 8

// rollouts is the rollout-*.jsonl files in the newest dayFolders non-empty
// day folders under dir, most recently modified first, at most maxRollouts.
func rollouts(dir string) []rolloutFile {
	if dir == "" {
		return nil
	}
	var out []rolloutFile
	seen := 0
	collect := func() []rolloutFile {
		sort.SliceStable(out, func(i, j int) bool {
			if !out[i].mod.Equal(out[j].mod) {
				return out[i].mod.After(out[j].mod)
			}
			return filepath.Base(out[i].path) > filepath.Base(out[j].path)
		})
		return out[:min(len(out), maxRollouts)]
	}
	for _, year := range datedDirs(dir, 4) {
		for _, month := range datedDirs(year, 2) {
			for _, day := range datedDirs(month, 2) {
				files := rolloutsIn(day)
				if len(files) == 0 {
					continue
				}
				out = append(out, files...)
				if seen++; seen >= dayFolders {
					return collect()
				}
			}
		}
	}
	return collect()
}

// datedDirs is dir's subdirectories named by width digits, newest first.
func datedDirs(dir string, width int) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() && len(e.Name()) == width && allDigits(e.Name()) {
			names = append(names, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = filepath.Join(dir, n)
	}
	return out
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// rolloutsIn is the regular rollout-*.jsonl files in dir.
func rolloutsIn(dir string) []rolloutFile {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []rolloutFile
	for _, e := range entries {
		name := e.Name()
		if !e.Type().IsRegular() || !strings.HasPrefix(name, "rollout-") || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, rolloutFile{filepath.Join(dir, name), info.ModTime()})
	}
	return out
}

// String is the quota for logs and tests.
func (q Quota) String() string {
	if !q.Known {
		return q.Provider + " unknown (" + q.Reason + ")"
	}
	parts := make([]string, len(q.Windows))
	for i, w := range q.Windows {
		if w.Known() {
			parts[i] = fmt.Sprintf("%s %g%%", w.Label, w.LeftPercent)
		} else {
			parts[i] = w.Label + " unknown"
		}
	}
	return q.Provider + " " + strings.Join(parts, " ") + " left"
}
