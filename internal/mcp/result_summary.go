package mcp

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// The Marshal chat is the provider's own CLI, which prints each MCP tool
// result under the call ("• Called marshal.run_digest └ {"run_id":…").
// MARSHAL cannot redraw that view, but the result is its own: a Marshal
// result starts with one plain sentence, so the line the person sees is
// that sentence and not internal IDs. The data follows unchanged, so the
// model still reads every field it parses.

// codexMCPClient is the clientInfo name Codex sends in initialize.
const codexMCPClient = "codex-mcp-client"

// resultDetailsURI names the data block of a summarised result when it is
// sent as an embedded resource.
const resultDetailsURI = "marshal://details"

// marshalResultContent is the MCP content for one successful tool result.
// Only the Marshal role's results are summarised; the project-contract
// tools keep their single block (they carry a fixed wire budget and a
// strict reader). For Codex the data block is an embedded resource: Codex
// shows such a block as one "embedded resource" line instead of printing
// the JSON, and still passes its full text to the model.
func (s *Server) marshalResultContent(name, text string) []map[string]any {
	data := map[string]any{"type": "text", "text": text}
	if s.role != "marshal" || strings.HasPrefix(name, "marshal_project_contract_") {
		return []map[string]any{data}
	}
	summary := toolResultSummary(name, text)
	if summary == "" {
		return []map[string]any{data}
	}
	if s.codexClient {
		data = map[string]any{"type": "resource", "resource": map[string]any{"uri": resultDetailsURI, "mimeType": "application/json", "text": text}}
	}
	return []map[string]any{{"type": "text", "text": summary}, data}
}

// noteMCPClient remembers which client opened this stdio session.
func (s *Server) noteMCPClient(params json.RawMessage) {
	var p struct {
		ClientInfo struct {
			Name string `json:"name"`
		} `json:"clientInfo"`
	}
	_ = json.Unmarshal(params, &p)
	s.codexClient = p.ClientInfo.Name == codexMCPClient
}

// toolResultSummary is one plain sentence for a JSON tool result, with no
// internal IDs, or "" for a tool that keeps its single block. Only the tools
// whose results are small records of state are summarised; reads that carry
// their own wire budget (memory recall, knowledge, project contract) are
// left exactly as they were.
func toolResultSummary(name, text string) string {
	var v any
	if json.Unmarshal([]byte(text), &v) != nil {
		return ""
	}
	if list, ok := v.([]any); ok {
		if name == "tasks_list" {
			return countPhrase(len(list), "task") + "."
		}
		return ""
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	state, _ := obj["state"].(string)
	switch name {
	case "run_digest":
		return runDigestSummary(obj)
	case "marshal_proposal_submit", "marshal_proposal_receipt":
		return proposalReceiptSummary(state)
	case "marshal_memory_candidate_submit", "marshal_memory_receipt", "memory_remember":
		return memoryReceiptSummary(state)
	case "task_get":
		status, _ := obj["status"].(string)
		if title, _ := obj["title"].(string); status != "" && title != "" && !strings.ContainsAny(title, "\n\r") && len(title) <= 60 {
			return fmt.Sprintf("Task %q is %s.", title, words(status))
		}
		return "Task found."
	}
	return ""
}

func runDigestSummary(v map[string]any) string {
	if noRun, _ := v["no_run"].(bool); noRun {
		return "No run yet."
	}
	tasks, _ := v["tasks"].([]any)
	done := 0
	for _, t := range tasks {
		task, _ := t.(map[string]any)
		switch state, _ := task["state"].(string); marshal.TaskState(state) {
		case marshal.Accepted, marshal.Merged:
			done++
		}
	}
	state, _ := v["state"].(string)
	head := "The run is " + words(state)
	if state == "" {
		head = "The run"
	}
	if paused, _ := v["paused"].(string); paused != "" {
		head = "The run is paused"
	}
	if len(tasks) == 0 {
		return head + "; no tasks yet."
	}
	return fmt.Sprintf("%s: %d of %s done.", head, done, countPhrase(len(tasks), "task"))
}

func proposalReceiptSummary(state string) string {
	switch state {
	case "admitted":
		return "Proposal received; it waits for the person's decision."
	case "applied":
		return "Proposal applied."
	case "rejected":
		return "Proposal not applied."
	case "answer_accepted":
		return "The person's answer was recorded."
	case "":
		return "Proposal sent."
	}
	return "Proposal " + words(state) + "."
}

func memoryReceiptSummary(state string) string {
	switch state {
	case "committed", "applied":
		return "Memory candidate saved."
	case "pending_permission":
		return "Memory candidate waits for permission."
	case "denied", "rejected":
		return "Memory candidate not saved."
	case "":
		return "Memory candidate sent."
	}
	return "Memory candidate " + words(state) + "."
}

// words is a state as plain words: "awaiting_user" reads "awaiting user".
func words(state string) string {
	return strings.ReplaceAll(state, "_", " ")
}

func countPhrase(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
