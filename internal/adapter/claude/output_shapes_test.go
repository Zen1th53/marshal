package claude

import (
	"context"
	"os"
	"testing"

	"github.com/Zen1th53/marshal/internal/adapter"
	"github.com/Zen1th53/marshal/internal/model"
)

// The recorded fixtures mirror Claude CLI 2.1.318 output for the harness
// probe: init, assistant, a rate_limit_event with status "allowed", and a
// successful result. With "verbose": true in the user's settings,
// --output-format json prints all four as one JSON array.
func TestRunReadsEveryClaudeOutputShape(t *testing.T) {
	for _, fixture := range []string{"testdata/probe-verbose-json.json", "testdata/probe-stream-json.jsonl"} {
		t.Run(fixture, func(t *testing.T) {
			stdout, err := os.ReadFile(fixture)
			if err != nil {
				t.Fatal(err)
			}
			client := New("claude", &mockRunner{runFunc: func(context.Context, adapter.Command) (adapter.ProcessResult, error) {
				return adapter.ProcessResult{ExitCode: 0, Stdout: stdout,
					Stderr:    []byte("Warning: this sign-in can end at any time from now until 2026-11-06\n"),
					Isolation: model.IsolationCapability{Level: model.IsolationBwrap, Available: true}}, nil
			}})
			result, err := client.Run(context.Background(), adapter.Request{TaskID: "PROBE-1", Title: "Reply exactly MARSHAL_PROBE_OK", Worktree: "/tmp/w"})
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != adapter.StatusSuccess || result.FinalText != "MARSHAL_PROBE_OK" {
				t.Fatalf("status=%s final=%.80q", result.Status, result.FinalText)
			}
			if result.SessionID != "51da6ec6-cf20-432a-85ff-385ae2ec34f6" || len(result.Events) != 4 {
				t.Fatalf("session=%q events=%d", result.SessionID, len(result.Events))
			}
		})
	}
}

func TestDecodeOutputEventsShapes(t *testing.T) {
	cases := map[string]int{
		`{"type":"result","result":"x"}`:                         1,
		`[{"type":"system"},{"type":"result","result":"x"}]`:     2,
		"{\"type\":\"system\"}\nnot json\n{\"type\":\"result\"}": 2,
		"[\n  {\"type\": \"result\", \"result\": \"x\"}\n]":      1,
		"plain text": 0,
	}
	for input, want := range cases {
		if got := len(DecodeOutputEvents([]byte(input))); got != want {
			t.Fatalf("%q: %d events, want %d", input, got, want)
		}
	}
}

func TestResultEventWinsOverEarlierResultField(t *testing.T) {
	var result adapter.Result
	parseClaudeOutput([]byte(`[{"type":"user","result":"tool output"},{"type":"result","result":"final"},{"type":"other","result":"late"}]`), &result)
	if result.FinalText != "final" {
		t.Fatalf("final=%q", result.FinalText)
	}
}

func TestClassifyStreamLineSeesVerboseArrayResult(t *testing.T) {
	stdout, err := os.ReadFile("testdata/probe-verbose-json.json")
	if err != nil {
		t.Fatal(err)
	}
	if ClassifyStreamLine(stdout[:len(stdout)-1]) != adapter.SignalDone {
		t.Fatal("verbose array with a successful result not seen as done")
	}
	if ClassifyStreamLine([]byte(`[{"type":"result","is_error":true}]`)) == adapter.SignalDone {
		t.Fatal("error result seen as done")
	}
}
