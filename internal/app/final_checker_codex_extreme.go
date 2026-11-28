//go:build !governed

package app

import (
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/proposalmcp"
)

// Codex exec exposes its thread on stdout and its actual turn model in the
// native rollout. Bind the rollout to that thread, workspace and launch time;
// never infer an identity from configuration or the requested model selector.
func requiredCodexRolloutModel(session, workspace string, started, finished time.Time) (string, []byte, bool) {
	if session == "" {
		return "", nil, false
	}
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return "", nil, false
		}
		home = filepath.Join(userHome, ".codex")
	}
	var model string
	var evidence []byte
	invalid := false
	for day := started.UTC().Truncate(24 * time.Hour); !day.After(finished.UTC()); day = day.Add(24 * time.Hour) {
		root := filepath.Join(home, "sessions", day.Format("2006/01/02"))
		_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil || entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(entry.Name(), "-"+session+".jsonl") {
				return nil
			}
			info, err := entry.Info()
			if err != nil || !info.Mode().IsRegular() || info.Size() > 32<<20 || info.ModTime().Before(started) {
				return nil
			}
			file, err := os.Open(path)
			if err != nil {
				invalid = true
				return nil
			}
			raw, err := io.ReadAll(io.LimitReader(file, (32<<20)+1))
			after, statErr := file.Stat()
			_ = file.Close()
			if err != nil || statErr != nil || len(raw) > 32<<20 || !os.SameFile(info, after) || info.Size() != after.Size() || !info.ModTime().Equal(after.ModTime()) {
				invalid = true
				return nil
			}
			lines := bytes.Split(bytes.TrimSpace(raw), []byte("\n"))
			if len(lines) == 0 {
				return nil
			}
			var meta struct {
				Type      string    `json:"type"`
				Timestamp time.Time `json:"timestamp"`
				Payload   struct {
					ID  string `json:"id"`
					CWD string `json:"cwd"`
				} `json:"payload"`
			}
			var metadata map[string]json.RawMessage
			if proposalmcp.DecodeStrict(lines[0], &metadata) != nil || json.Unmarshal(lines[0], &meta) != nil || meta.Type != "session_meta" || meta.Payload.ID != session || filepath.Clean(meta.Payload.CWD) != filepath.Clean(workspace) || meta.Timestamp.Before(started) || meta.Timestamp.After(finished) {
				invalid = true
				return nil
			}
			found := ""
			for _, line := range lines[1:] {
				var record map[string]json.RawMessage
				if proposalmcp.DecodeStrict(line, &record) != nil {
					invalid = true
					return nil
				}
				var kind string
				_ = json.Unmarshal(record["type"], &kind)
				if kind != "turn_context" {
					continue
				}
				var turn struct {
					Model string `json:"model"`
					CWD   string `json:"cwd"`
				}
				if json.Unmarshal(record["payload"], &turn) != nil || turn.Model == "" || (turn.CWD != "" && filepath.Clean(turn.CWD) != filepath.Clean(workspace)) {
					invalid = true
					return nil
				}
				if found != "" && found != turn.Model {
					invalid = true
					return nil
				}
				found = turn.Model
			}
			if found == "" || evidence != nil {
				invalid = true
				return nil
			}
			// Apply the same syntax and conflict rules as native stream metadata.
			event, _ := json.Marshal(map[string]string{"type": "thread.started", "model": found})
			model = requiredNativeStreamModel("codex", event)
			evidence = raw
			return nil
		})
	}
	if invalid {
		return "", nil, true
	}
	return model, evidence, false
}
