//go:build !governed

package app

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
)

func MarshalProviders() []string { return []string{"codex", "claude", "agy", "opencode"} }

// The native AGY CLI accepts --json-schema, but the model can still answer as
// Markdown with a different field convention. Send the exact contract in the
// model's prompt as well. Invalid results remain refused; this creates neither
// an execution grant nor a retry and never rewrites a received verdict.
func marshalStructuredResultPrompt(provider, prompt, schema string) string {
	if provider != "agy" {
		return prompt
	}
	return prompt + "\nReturn exactly one bare JSON object matching the following schema. Use each field's exact spelling and case; do not rename fields, use snake_case aliases, add Markdown fences, commentary, or extra objects. Schema:\n" + schema
}

func buildMarshalTurn(provider, prompt, schema, session string) ([]string, bool) {
	if provider != "opencode" {
		return nil, false
	}
	args := []string{"run", "--format", "json", "--agent", "plan"}
	if session != "" {
		args = append(args, "--session", session)
	}
	return append(args, prompt+"\nReturn only JSON matching this schema: "+schema), true
}

func buildMarshalOutput(provider string, data []byte) ([]byte, string, bool, error) {
	if provider != "opencode" {
		return nil, "", false, nil
	}
	var session, output string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var event struct {
			Type      string `json:"type"`
			SessionID string `json:"sessionID"`
			Part      struct {
				Text string `json:"text"`
			} `json:"part"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return nil, "", true, err
		}
		if event.SessionID != "" {
			session = event.SessionID
		}
		if event.Type == "text" {
			output += event.Part.Text
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, "", true, err
	}
	if output == "" {
		return nil, "", true, fmt.Errorf("opencode produced no structured Marshal output")
	}
	return []byte(output), session, true, nil
}
