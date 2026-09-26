package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/Zen1th53/marshal/internal/marshal"
)

// MarshalCLI runs one structured, noninteractive model turn at a time.
type MarshalCLI struct {
	Provider       string
	Binary         string
	Dir            string
	ConversationID string
}

func (m *MarshalCLI) MarshalConversationID() string {
	if m == nil {
		return ""
	}
	return m.ConversationID
}
func (m *MarshalCLI) SetMarshalConversationID(id string) {
	if m != nil {
		m.ConversationID = id
	}
}

const marshalDraftSchema = `{"type":"object","properties":{"plan":{"type":"object"},"tasks":{"type":"array"}},"required":["plan","tasks"]}`
const marshalReviewSchema = `{"type":"object","properties":{"Verdict":{"type":"string"},"Reviewer":{"type":"string"},"Reasons":{"type":"array"},"EvidenceRefs":{"type":"array"}},"required":["Verdict","Reviewer"]}`

func (m *MarshalCLI) turn(ctx context.Context, prompt, schema string, out any) error {
	if m == nil {
		return errors.New("Marshal CLI is unavailable")
	}
	binary := m.Binary
	if binary == "" {
		binary = m.Provider
	}
	var args []string
	switch m.Provider {
	case "claude":
		args = []string{"-p", prompt, "--output-format", "json", "--json-schema", schema, "--restricted"}
		if m.ConversationID != "" {
			args = append(args, "--resume", m.ConversationID)
		}
	case "agy":
		args = []string{"-p", prompt, "--output-format", "json", "--json-schema", schema, "--mode", "plan"}
		if m.ConversationID != "" {
			args = append(args, "--conversation", m.ConversationID)
		}
	case "codex":
		file, err := os.CreateTemp("", "marshal-model-schema-*.json")
		if err != nil {
			return err
		}
		defer os.Remove(file.Name())
		if _, err = file.WriteString(schema); err != nil {
			file.Close()
			return err
		}
		if err = file.Close(); err != nil {
			return err
		}
		if m.ConversationID == "" {
			args = []string{"exec", "--json", "-s", "read-only", "--output-schema", file.Name(), prompt}
		} else {
			args = []string{"exec", "resume", "--json", "--output-schema", file.Name(), m.ConversationID, prompt}
		}
	default:
		return fmt.Errorf("unsupported Marshal provider %q", m.Provider)
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = m.Dir
	data, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("Marshal model turn: %w", err)
	}
	payload, id, err := marshalCLIOutput(m.Provider, data)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(payload, out); err != nil {
		return fmt.Errorf("Marshal model JSON: %w", err)
	}
	if id != "" {
		m.ConversationID = id
	}
	return nil
}

func marshalCLIOutput(provider string, data []byte) ([]byte, string, error) {
	switch provider {
	case "claude":
		var result struct {
			SessionID  string          `json:"session_id"`
			Structured json.RawMessage `json:"structured_output"`
			Result     string          `json:"result"`
		}
		if err := json.Unmarshal(data, &result); err != nil {
			return nil, "", err
		}
		if len(result.Structured) > 0 {
			return result.Structured, result.SessionID, nil
		}
		if result.Result != "" {
			return []byte(result.Result), result.SessionID, nil
		}
	case "agy":
		var result struct {
			ConversationID string `json:"conversation_id"`
			Response       string `json:"response"`
			Result         struct {
				ConversationID string `json:"conversation_id"`
				Response       string `json:"response"`
			} `json:"result"`
		}
		if err := json.Unmarshal(data, &result); err != nil {
			return nil, "", err
		}
		if result.Result.Response != "" {
			return []byte(result.Result.Response), result.Result.ConversationID, nil
		}
		if result.Response != "" {
			return []byte(result.Response), result.ConversationID, nil
		}
	case "codex":
		var session, text string
		scanner := bufio.NewScanner(bytes.NewReader(data))
		scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
		for scanner.Scan() {
			var event struct {
				Type     string `json:"type"`
				ThreadID string `json:"thread_id"`
				Item     struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"item"`
			}
			if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
				return nil, "", err
			}
			if event.Type == "thread.started" {
				session = event.ThreadID
			}
			if event.Type == "item.completed" && event.Item.Type == "agent_message" {
				text = event.Item.Text
			}
		}
		if err := scanner.Err(); err != nil {
			return nil, "", err
		}
		if text != "" {
			return []byte(text), session, nil
		}
	}
	return nil, "", fmt.Errorf("%s produced no structured Marshal output", strings.TrimSpace(provider))
}

func (m *MarshalCLI) Draft(ctx context.Context, goal string) (MarshalDraft, error) {
	var out MarshalDraft
	err := m.turn(ctx, "Return a Marshal plan as JSON for: "+goal, marshalDraftSchema, &out)
	return out, err
}
func (m *MarshalCLI) Review(ctx context.Context, task marshal.Task, handin marshal.HandIn) (marshal.Review, error) {
	var out marshal.Review
	input, _ := json.Marshal(struct {
		Task   marshal.Task
		HandIn marshal.HandIn
	}{task, handin})
	err := m.turn(ctx, "Review this hand-in and return a JSON verdict: "+string(input), marshalReviewSchema, &out)
	return out, err
}
func (m *MarshalCLI) Amend(ctx context.Context, run marshal.Run, reason string) (MarshalDraft, error) {
	var out MarshalDraft
	input, _ := json.Marshal(run)
	err := m.turn(ctx, "Propose a JSON plan amendment for "+reason+": "+string(input), marshalDraftSchema, &out)
	return out, err
}
