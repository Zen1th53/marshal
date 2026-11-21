package mcp

import (
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/marshalsteps"
)

// localSteps names the steps of the tools this server answers itself for
// the live Marshal. The proposal tools go to the Board's broker, which notes
// their steps there; everything else the Marshal calls is noted here, so
// the Board's steps pane sees every MARSHAL tool call it makes.
var localSteps = map[string][2]string{
	"project_search":   {"read", "project search"},
	"project_overview": {"read", "project overview"},
	"project_context":  {"read", "project context"},
	"related_files":    {"read", "related files"},
	"find_symbol":      {"read", "find symbol"},
	"memory_recall":    {"read", "memory recall"},
	"memory_status":    {"read", "memory status"},
	"marshal_status":   {"read", "runtime status"},
	"task_get":         {"read", "a task"},
	"events_page":      {"read", "events"},
}

// localStep is a locally served tool's kind and line: the tool's name only,
// never its arguments.
func localStep(name string) (string, string) {
	if st, ok := localSteps[name]; ok {
		return st[0], st[1]
	}
	name = strings.TrimPrefix(strings.TrimSpace(name), "marshal_")
	if name == "" || len(name) > 40 || strings.ContainsFunc(name, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_')
	}) {
		return "check", "a MARSHAL tool"
	}
	kind := "check"
	if strings.Contains(name, "search") || strings.Contains(name, "get") || strings.Contains(name, "read") || strings.Contains(name, "list") || strings.Contains(name, "history") {
		kind = "read"
	}
	return kind, strings.ReplaceAll(name, "_", " ")
}

// noteMarshalStep appends one step for a tool call this server answered
// for the live Marshal to the step log in the project's state directory.
// It is best effort: a log that cannot be written never fails the call.
func (s *Server) noteMarshalStep(name string, err error) {
	if s.role != "marshal" || s.runtime == nil {
		return
	}
	if name == "memory_remember" && s.proposalCall != nil {
		// Sent on to the broker, which notes it as a memory submission.
		return
	}
	dir := s.runtime.StateDirectory()
	if dir == "" {
		return
	}
	kind, text := localStep(name)
	if err != nil {
		text += " · failed"
	}
	_ = marshalsteps.Append(marshalsteps.Path(dir), marshalsteps.Line{At: time.Now(), Kind: kind, Text: text})
}
