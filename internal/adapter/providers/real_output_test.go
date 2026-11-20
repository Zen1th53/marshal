package providers

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/adapter"
)

// realCLI answers with what each installed CLI printed (testdata): codex
// 0.169.0 `codex debug models` (trimmed to four models, all fields Marshal
// reads kept verbatim), agy 1.5.10 `agy models`, claude 2.1.329 `--help`
// with a stream-json init event, and opencode's provider/model listing.
type realCLI struct{ t *testing.T }

func (r realCLI) Run(_ context.Context, c adapter.Command) (adapter.ProcessResult, error) {
	read := func(name string) []byte {
		data, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			r.t.Fatal(err)
		}
		return data
	}
	args := strings.Join(c.Args, " ")
	switch filepath.Base(c.Path) + " " + args {
	case "codex debug models":
		return adapter.ProcessResult{Stdout: read("codex-debug-models.json")}, nil
	case "agy models":
		return adapter.ProcessResult{Stdout: read("agy-models.txt")}, nil
	case "opencode models":
		return adapter.ProcessResult{Stdout: read("opencode-models.txt")}, nil
	case "claude --help":
		return adapter.ProcessResult{Stdout: read("claude-help.txt")}, nil
	}
	if filepath.Base(c.Path) == "claude" {
		return adapter.ProcessResult{Stdout: []byte(`{"type":"system","subtype":"init","session_id":"s","model":"claude-opus-5-5"}` + "\n")}, nil
	}
	return adapter.ProcessResult{ExitCode: 2}, nil
}

// Every registered provider's catalog is what its installed CLI printed:
// the models, the default it names, and the efforts it advertises.
func TestEveryProviderReadsItsRealCLIOutput(t *testing.T) {
	isolate(t)
	want := map[string]struct {
		models, def, efforts string // efforts of the first model
	}{
		"codex":    {"gpt-6.2-sol,gpt-6.1-terra,gpt-reserve,gpt-6-luna", "gpt-6.2-sol", "low,medium,high,xhigh,max,ultra"},
		"claude":   {"fable,opus,sonnet,claude-opus-5-5", "claude-opus-5-5", "low,medium,high,xhigh,max"},
		"opencode": {"anthropic/claude-sonnet-4-5,anthropic/claude-opus-4-1,openai/gpt-5,ollama/qwen2.5-coder:7b", "", ""},
		"agy":      {"", "", ""}, // checked below: the full listing
	}
	for _, p := range All() {
		id := p.Spec().ID
		t.Run(id, func(t *testing.T) {
			w, ok := want[id]
			if !ok {
				t.Fatalf("no real CLI output for %s in testdata", id)
			}
			catalog, err := p.Models(context.Background(), "/real/"+p.Spec().Binary, realCLI{t})
			if err != nil {
				t.Fatal(err)
			}
			var ids []string
			for _, m := range catalog.Models {
				ids = append(ids, m.ID)
			}
			if id == "agy" {
				if len(ids) != 23 || ids[0] != "gemini-4-neon-high" || catalog.Default != "" {
					t.Fatalf("agy catalog = %v default %q", ids, catalog.Default)
				}
				return
			}
			if strings.Join(ids, ",") != w.models || catalog.Default != w.def {
				t.Fatalf("models = %v default %q", ids, catalog.Default)
			}
			if got := strings.Join(catalog.Models[0].Efforts, ","); got != w.efforts {
				t.Fatalf("efforts = %s, want %s", got, w.efforts)
			}
		})
	}
}
