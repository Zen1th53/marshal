package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestMarshalCLIDefaultBinaryRunsSelectedProvider(t *testing.T) {
	root := t.TempDir()
	stub := `#!/bin/sh
printf '%s\n' '{"type":"thread.started","thread_id":"thread-test"}'
printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"{\"ok\":true}"}}'
`
	if err := os.WriteFile(filepath.Join(root, "codex"), []byte(stub), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	cli := &MarshalCLI{Provider: "codex", Dir: root}
	var output struct {
		OK bool `json:"ok"`
	}
	if err := cli.turn(context.Background(), "test", marshalDraftSchema, &output); err != nil {
		t.Fatal(err)
	}
	if !output.OK || cli.ConversationID != "thread-test" {
		t.Fatalf("default provider binary did not return a model turn: %+v session=%q", output, cli.ConversationID)
	}
}
