//go:build !governed

package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFinalCheckCodexRolloutBinding(t *testing.T) {
	for _, fault := range []string{"", "session", "workspace", "old", "conflict", "duplicate", "missing"} {
		t.Run(fault, func(t *testing.T) {
			home, workspace := t.TempDir(), t.TempDir()
			t.Setenv("CODEX_HOME", home)
			started := time.Now().UTC().Add(-time.Second)
			stamp := started.Add(time.Millisecond)
			id, cwd := "observed-session", workspace
			if fault == "session" {
				id = "other"
			}
			if fault == "workspace" {
				cwd = t.TempDir()
			}
			if fault == "old" {
				stamp = started.Add(-time.Second)
			}
			meta, _ := json.Marshal(map[string]any{"type": "session_meta", "timestamp": stamp, "payload": map[string]string{"id": id, "cwd": cwd, "cli_version": "fixture"}})
			turn, _ := json.Marshal(map[string]any{"type": "turn_context", "payload": map[string]string{"model": "observed-model", "cwd": workspace, "effort": "high"}})
			raw := append(append(meta, '\n'), turn...)
			raw = append(raw, '\n')
			if fault == "conflict" {
				raw = append(raw, []byte("{\"type\":\"turn_context\",\"payload\":{\"model\":\"other\"}}\n")...)
			}
			if fault == "duplicate" {
				raw = append(raw, []byte("{\"type\":\"turn_context\",\"payload\":{\"model\":\"a\",\"model\":\"b\"}}\n")...)
			}
			if fault == "missing" {
				raw = append(meta, '\n')
			}
			root := filepath.Join(home, "sessions", started.Format("2006/01/02"))
			if err := os.MkdirAll(root, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "rollout-observed-session.jsonl"), raw, 0600); err != nil {
				t.Fatal(err)
			}
			model, evidence, _ := requiredCodexRolloutModel("observed-session", workspace, started, time.Now().UTC())
			if fault == "" {
				if model != "observed-model" || len(evidence) == 0 {
					t.Fatalf("native identity missing: %q", model)
				}
			} else if model != "" {
				t.Fatalf("%s binding accepted: %q", fault, model)
			}
		})
	}
}
