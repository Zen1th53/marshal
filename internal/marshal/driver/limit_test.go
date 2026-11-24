package driver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
)

const terminalRecord = `{"type":"item.completed","item":{"type":"command_execution","command":"final check","aggregated_output":"ok","exit_code":0}}`

// streamOfSize is a stream of filler lines that ends with record and is
// exactly size bytes long.
func streamOfSize(size int, record string) []byte {
	body := size - len(record) - 1
	var b strings.Builder
	for body > 0 {
		n := min(body, 4096)
		b.WriteString(strings.Repeat("x", n-1))
		b.WriteString("\n")
		body -= n
	}
	return []byte(b.String() + record + "\n")
}

// A worker stream at its cap is parsed whole; one byte over, the head is
// bounded and the final record is still parsed. A malformed or overlong
// final record makes the worker account incomplete with a typed limit.
func TestBoundedStreamPreservesTerminalReceipt(t *testing.T) {
	for _, size := range []int{maxStream, maxStream + 1} {
		buf := newStreamBuffer(maxStream)
		stream := streamOfSize(size, terminalRecord)
		if len(stream) != size {
			t.Fatalf("fixture size %d, want %d", len(stream), size)
		}
		// Written in pieces, as a process writes it.
		for len(stream) > 0 {
			n := min(len(stream), 32<<10)
			_, _ = buf.Write(stream[:n])
			stream = stream[n:]
		}
		reported, limits := buf.result(parseCodex)
		if len(reported) != 1 || reported[0].Command != "final check" || reported[0].ExitCode != 0 {
			t.Fatalf("size %d: final record lost: %+v", size, reported)
		}
		if size == maxStream && len(limits) != 0 {
			t.Fatalf("stream at its cap was limited: %+v", limits)
		}
		if size > maxStream {
			if len(limits) != 1 || limits[0].Kind != "worker_stream" || !limits[0].Limited || limits[0].ActualBytes != int64(size) || limits[0].CapBytes != maxStream || limits[0].Action == "" {
				t.Fatalf("size %d: limits = %+v", size, limits)
			}
		}
	}
	for name, record := range map[string]string{
		"malformed": `{"type":"item.completed"`,
		"overlong":  `{"type":"x","pad":"` + strings.Repeat("y", maxTerminalRecord) + `"}`,
	} {
		buf := newStreamBuffer(maxStream)
		_, _ = buf.Write(streamOfSize(maxStream+1, record))
		_, limits := buf.result(parseCodex)
		if len(limits) != 1 || limits[0].Kind != "worker_stream_incomplete" || limits[0].Action == "" {
			t.Fatalf("%s final record: limits = %+v", name, limits)
		}
	}
}

// One overlong record does not hide the records after it.
func TestStreamRecordsParsedOneByOne(t *testing.T) {
	stream := `{"pad":"` + strings.Repeat("z", maxStream) + `"` + "\n" + terminalRecord + "\n"
	if got := parseCodex([]byte(stream)); len(got) != 1 {
		t.Fatalf("records after an overlong one were lost: %+v", got)
	}
}

// git output at its cap is kept; one byte over is a typed limit with the
// exact size, never a shortened output.
func TestGitOutputLimitAtCap(t *testing.T) {
	_, wt := newTask(t)
	for _, size := range []int{maxGitOutput, maxGitOutput + 1} {
		path := filepath.Join(wt, "blob.txt")
		if err := os.WriteFile(path, []byte(strings.Repeat("x", size)), 0o644); err != nil {
			t.Fatal(err)
		}
		blob := run(t, wt, "git", "hash-object", "-w", "blob.txt")
		out, err := git(context.Background(), wt, "cat-file", "-p", blob)
		if size == maxGitOutput {
			if err != nil || len(out) != size {
				t.Fatalf("output at cap: %d bytes, %v", len(out), err)
			}
			continue
		}
		limit, ok := model.LimitOf(err)
		if !errors.Is(err, ErrHandInTooLarge) || !ok || limit.ActualBytes != int64(size) || limit.CapBytes != maxGitOutput || out != "" {
			t.Fatalf("output over cap: %q, %v, %+v", out[:min(len(out), 10)], err, limit)
		}
	}
}

// A diff over the cap is handed in by reference: no diff text, the verified
// base and result commits, every changed path and a per-file action.
func TestOversizedDiffHandInKeepsPointer(t *testing.T) {
	task, wt := newTask(t)
	task.Files = []string{"big.txt", "real.txt"}
	g := Governed{Run: func(_ context.Context, req Request) ([]marshal.CommandRecord, error) {
		if err := os.WriteFile(filepath.Join(req.Worktree, "real.txt"), []byte("small\n"), 0o644); err != nil {
			return nil, err
		}
		return nil, os.WriteFile(filepath.Join(req.Worktree, "big.txt"), []byte(strings.Repeat("line of text\n", maxGitOutput/13+1)), 0o644)
	}}
	out := handIn(t, g, Request{Task: task, Worktree: wt, Brief: "edit"})
	if out.Diff != "" || len(out.FilesTouched) != 2 || out.ResultCommit == "" || out.BaseCommit != task.BaseCommit {
		t.Fatalf("hand-in = diff %d bytes, files %v", len(out.Diff), out.FilesTouched)
	}
	if len(out.Limits) != 1 || out.Limits[0].Kind != "git_diff" || out.Limits[0].EvidenceRef != "result:"+out.ResultCommit ||
		out.Limits[0].ActualBytes <= maxGitOutput || !strings.Contains(out.Limits[0].Action, out.ResultCommit+" -- <path>") || strings.Contains(out.Limits[0].Action, wt) {
		t.Fatalf("limits = %+v", out.Limits)
	}
}
