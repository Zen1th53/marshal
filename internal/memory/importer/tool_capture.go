package importer

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// Message kinds. An empty kind is ordinary visible conversation text, which is
// what every provider adapter emitted before tool capture existed.
const (
	MessageKindText       = ""
	MessageKindToolUse    = "tool_use"
	MessageKindToolResult = "tool_result"
)

// Capture bounds. Source the agent wrote or deleted is durable evidence of what
// actually changed, so it is kept whole up to maxToolCodeBytes. Tool results are
// transient console output: they carry most of the volume and the highest chance
// of sweeping an unrelated credential into memory, so they are cut hard.
const (
	maxToolResultBytes = 2048
	maxToolCodeBytes   = 64 << 10
	maxToolArgBytes    = 512
	maxToolTailArgs    = 6
)

// codeArgs name the tool inputs that carry source text. They are rendered as a
// diff rather than as arguments, because "what the agent changed" is the part a
// later session needs and a JSON blob buries it.
var codeArgs = map[string]string{
	"old_string":  "-",
	"old_str":     "-",
	"new_string":  "+",
	"new_str":     "+",
	"content":     "+",
	"new_source":  "+",
	"new_content": "+",
	"replacement": "+",
}

// headlineArgs identify what a call acted on. The first match becomes the
// call's headline, so an Edit reads as its path and a Bash call as its command.
var headlineArgs = []string{
	"file_path", "path", "notebook_path", "file",
	"command", "cmd", "script",
	"pattern", "query", "url", "prompt",
}

// noiseArgs add no cross-session value and are dropped rather than truncated.
var noiseArgs = map[string]bool{
	"description": true,
	"explanation": true,
	"tool_use_id": true,
	"call_id":     true,
	"id":          true,
}

// truncateForMemory cuts on a rune boundary and states the loss inline, so a
// later reader can never mistake a truncated payload for a complete one.
func truncateForMemory(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return fmt.Sprintf("%s\n… truncated (%d of %d bytes)", strings.TrimRight(s[:cut], "\n"), cut, len(s))
}

// decodeScalar renders one JSON value as text. Strings unquote; anything else
// keeps its JSON form so structure is not silently flattened away.
func decodeScalar(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}

// renderToolUse formats one tool invocation: the tool's name, the single
// argument that says what it acted on, the source it wrote or removed as a
// diff, and whatever scalar arguments remain.
func renderToolUse(name string, input json.RawMessage) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "(unnamed tool)"
	}
	fields := map[string]json.RawMessage{}
	if len(input) > 0 {
		// A non-object input is still worth recording as the call's headline.
		if err := json.Unmarshal(input, &fields); err != nil {
			if scalar := strings.TrimSpace(decodeScalar(input)); scalar != "" {
				return name + " " + truncateForMemory(scalar, maxToolArgBytes)
			}
			return name
		}
	}

	var b strings.Builder
	b.WriteString(name)

	headline := ""
	for _, key := range headlineArgs {
		raw, ok := fields[key]
		if !ok {
			continue
		}
		if value := strings.TrimSpace(decodeScalar(raw)); value != "" {
			headline = key
			b.WriteString(" ")
			b.WriteString(truncateForMemory(value, maxToolArgBytes))
			break
		}
	}

	// Diff lines, oldest marker first, so a replacement reads as - then +.
	for _, key := range []string{"old_string", "old_str", "new_string", "new_str", "content", "new_source", "new_content", "replacement"} {
		raw, ok := fields[key]
		if !ok {
			continue
		}
		code := strings.TrimRight(decodeScalar(raw), "\n")
		if strings.TrimSpace(code) == "" {
			continue
		}
		marker := codeArgs[key]
		for _, line := range strings.Split(truncateForMemory(code, maxToolCodeBytes), "\n") {
			b.WriteString("\n  ")
			b.WriteString(marker)
			b.WriteString(" ")
			b.WriteString(line)
		}
	}

	var tail []string
	for key, raw := range fields {
		if key == headline || codeArgs[key] != "" || noiseArgs[key] {
			continue
		}
		value := strings.TrimSpace(decodeScalar(raw))
		if value == "" {
			continue
		}
		tail = append(tail, fmt.Sprintf("%s=%s", key, truncateForMemory(value, maxToolArgBytes)))
	}
	if len(tail) > 0 {
		// Map iteration is random; a stable record keeps the content digest
		// stable so repeat imports deduplicate instead of piling up.
		sort.Strings(tail)
		if len(tail) > maxToolTailArgs {
			tail = append(tail[:maxToolTailArgs:maxToolTailArgs], fmt.Sprintf("… %d more argument(s)", len(tail)-maxToolTailArgs))
		}
		b.WriteString("\n  ")
		b.WriteString(strings.Join(tail, " "))
	}
	return b.String()
}

// renderToolResult formats one tool result with its true size, so truncation is
// visible and an error result is never mistaken for a successful one.
func renderToolResult(output string, isError bool) string {
	output = strings.TrimSpace(output)
	if output == "" {
		if isError {
			return "ERROR (no output)"
		}
		return ""
	}
	var b strings.Builder
	if isError {
		b.WriteString("ERROR ")
	}
	if len(output) > maxToolResultBytes {
		fmt.Fprintf(&b, "(%d of %d bytes)\n", maxToolResultBytes, len(output))
	} else {
		fmt.Fprintf(&b, "(%d bytes)\n", len(output))
	}
	b.WriteString(truncateForMemory(output, maxToolResultBytes))
	return b.String()
}

// claudeToolResultText flattens Claude's tool_result content, which is either a
// plain string or a block list, into the text a later session can read.
func claudeToolResultText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var plain string
	if err := json.Unmarshal(raw, &plain); err == nil {
		return plain
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	var parts []string
	for _, block := range blocks {
		// Image and other binary blocks are recorded as presence, never bytes.
		if block.Type != "text" {
			if block.Type != "" {
				parts = append(parts, "("+block.Type+" block)")
			}
			continue
		}
		if strings.TrimSpace(block.Text) != "" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "\n")
}
