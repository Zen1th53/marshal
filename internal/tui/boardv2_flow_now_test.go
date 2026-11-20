package tui

import (
	"strings"
	"testing"
)

// flowNowLine prefers the Marshal's model as the top bar names it, and
// falls back to "Marshal" only when neither provider nor model is known.
func TestFlowNowLineNamesMarshalModel(t *testing.T) {
	in := flowIn{marshalProvider: "claude", marshalModel: "Opus 5.5", chatModel: "claude-opus-5-5"}
	if got := flowNowLine(in).Marshal; !strings.HasPrefix(got, "claude Opus 5.5") {
		t.Fatalf("NOW Marshal = %q", got)
	}
	if got := flowNowLine(flowIn{}).Marshal; !strings.HasPrefix(got, "Marshal") {
		t.Fatalf("NOW Marshal with nothing known = %q", got)
	}
}
