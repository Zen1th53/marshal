package app

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func marshalTurnFixture(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "provider")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

// marshalTurnOutput returns a directory the fixture script may write to. The
// provider must never write observations into the inspected project tree: the
// private build re-reads that tree after every admitted model turn.
func marshalTurnOutput(t *testing.T) string {
	t.Helper()
	out := t.TempDir()
	t.Setenv("MARSHAL_TEST_OUT", out)
	return out
}

func TestMarshalModelPromptTransportCodex(t *testing.T) {
	for _, session := range []string{"", "existing-thread"} {
		for _, size := range []int{8, transportLargePromptBytes} {
			name := "direct/small"
			if session != "" {
				name = "resume/small"
			}
			if size > 128*1024 {
				name = strings.Replace(name, "small", "large", 1)
			}
			t.Run(name, func(t *testing.T) {
				out := marshalTurnOutput(t)
				binary := marshalTurnFixture(t, `printf '%s\0' "$@" > "$MARSHAL_TEST_OUT/args"
while [ "$#" -gt 0 ]; do
 if [ "$1" = '--output-schema' ]; then
  shift
  printf '%s' "$1" > "$MARSHAL_TEST_OUT/schema-path"
  cat "$1" > "$MARSHAL_TEST_OUT/schema-copy" || exit 9
 fi
 shift
done
cat > "$MARSHAL_TEST_OUT/stdin"
printf '%s\n' '{"type":"thread.started","thread_id":"new-thread"}'
printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"{\"ok\":true}"}}'
`)
				prompt := strings.Repeat("e", size) + "\nUTF-8: 日本語\n"
				knowledge := strings.Repeat("context\n", size/8) + "final evidence"
				cli := &MarshalCLI{Provider: "codex", Binary: binary, ConversationID: session,
					ProjectContext: func(context.Context, string, string) (string, error) { return knowledge, nil }}
				ctx := requiredMarshalTestContext(t, cli)
				var result struct {
					OK bool `json:"ok"`
				}
				if err := cli.turn(ctx, prompt, marshalDraftSchema, &result); err != nil {
					t.Fatal(err)
				}
				if !result.OK || cli.ConversationID != "new-thread" {
					t.Fatalf("output/session: %+v %q", result, cli.ConversationID)
				}
				read := func(name string) string {
					t.Helper()
					b, err := os.ReadFile(filepath.Join(out, name))
					if err != nil {
						t.Fatal(err)
					}
					return string(b)
				}
				assertTransportPrompt(t, read("stdin"), prompt, knowledge)
				schemaPath := read("schema-path")
				if got := read("schema-copy"); got != marshalDraftSchema {
					t.Fatal("schema changed")
				}
				if _, err := os.Stat(schemaPath); !os.IsNotExist(err) {
					t.Fatalf("schema not removed: %v", err)
				}
				want := []string{"exec", "--json", "-s", "read-only", "--output-schema", schemaPath, "-"}
				if session != "" {
					want = []string{"exec", "resume", "--json", "-c", `sandbox_mode="read-only"`, "--output-schema", schemaPath, session, "-"}
				}
				if got := strings.Split(strings.TrimSuffix(read("args"), "\x00"), "\x00"); !reflect.DeepEqual(got, want) {
					t.Fatalf("args = %q, want %q", got, want)
				}
			})
		}
	}
}

func TestMarshalModelPromptTransportFailureAndCancellation(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(map[bool]string{false: "exit", true: "cancel"}[cancel], func(t *testing.T) {
			out := marshalTurnOutput(t)
			body := `while [ "$#" -gt 0 ]; do
 if [ "$1" = '--output-schema' ]; then shift; printf '%s' "$1" > "$MARSHAL_TEST_OUT/schema-path"; fi
 shift
done
`
			if cancel {
				// Wait without spinning: a provider orphaned by an aborted test
				// binary must not burn a CPU.
				body += "while :; do sleep 0.05; done\n"
			} else {
				body += "cat >/dev/null\nprintf 'fixture failure' >&2\nprintf '%5000s' '' >&2\nprintf 'end' >&2\nexit 7\n"
			}
			cli := &MarshalCLI{Provider: "codex", Binary: marshalTurnFixture(t, body), ConversationID: "original"}
			ctx := requiredMarshalTestContext(t, cli)
			if cancel {
				var stop context.CancelFunc
				// The deadline also covers the required-context admission
				// before the provider starts; 300ms was too short under load and
				// killed the provider before it recorded its schema path.
				ctx, stop = context.WithTimeout(ctx, 2*time.Second)
				defer stop()
			}
			start := time.Now()
			var result map[string]any
			err := cli.turn(ctx, strings.Repeat("x", transportLargePromptBytes), marshalDraftSchema, &result)
			if err == nil {
				t.Fatal("expected error")
			}
			if time.Since(start) > 5*time.Second {
				t.Fatal("failure was not bounded")
			}
			if !cancel && !strings.Contains(err.Error(), "fixture failure") {
				t.Fatalf("lost stderr: %v", err)
			}
			if !cancel && (len(err.Error()) > 4500 || !strings.Contains(err.Error(), "[truncated]")) {
				t.Fatalf("unbounded diagnostic: %d bytes", len(err.Error()))
			}
			if cli.ConversationID != "original" || result != nil {
				t.Fatal("failure changed output/session")
			}
			b, readErr := os.ReadFile(filepath.Join(out, "schema-path"))
			if readErr != nil {
				t.Fatal(readErr)
			}
			if _, statErr := os.Stat(string(b)); !os.IsNotExist(statErr) {
				t.Fatalf("schema leaked: %v", statErr)
			}
		})
	}
}

func TestMarshalModelPromptTransportOtherProviders(t *testing.T) {
	for _, provider := range []string{"claude", "agy"} {
		t.Run(provider, func(t *testing.T) {
			out := marshalTurnOutput(t)
			binary := marshalTurnFixture(t, `printf '%s\0' "$@" > "$MARSHAL_TEST_OUT/args"
cat > "$MARSHAL_TEST_OUT/stdin"
printf '%s\n' '{"status":"SUCCESS","session_id":"session","conversation_id":"session","structured_output":{"ok":true}}'
`)
			cli := &MarshalCLI{Provider: provider, Binary: binary, ConversationID: "existing"}
			ctx := requiredMarshalTestContext(t, cli)
			var result struct {
				OK bool `json:"ok"`
			}
			if err := cli.turn(ctx, "exact prompt", marshalDraftSchema, &result); err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(filepath.Join(out, "stdin"))
			if err != nil {
				t.Fatal(err)
			}
			if len(b) != 0 {
				t.Fatal("invented stdin transport")
			}
			b, err = os.ReadFile(filepath.Join(out, "args"))
			if err != nil {
				t.Fatal(err)
			}
			args := strings.Split(strings.TrimSuffix(string(b), "\x00"), "\x00")
			wantSuffix := []string{"--restricted", "--resume", "existing"}
			if provider == "agy" {
				wantSuffix = []string{"--mode", "plan", "--conversation", "existing"}
			}
			if len(args) != 6+len(wantSuffix) || args[0] != "-p" || !strings.Contains(args[1], "exact prompt") || args[2] != "--output-format" || args[3] != "json" || args[4] != "--json-schema" || args[5] != marshalDraftSchema {
				t.Fatalf("provider contract changed: %q", args)
			}
			if !reflect.DeepEqual(args[6:], wantSuffix) {
				t.Fatalf("provider options: %q", args[6:])
			}
			if !result.OK || cli.ConversationID != "session" {
				t.Fatal("output parsing changed")
			}
		})
	}
}
