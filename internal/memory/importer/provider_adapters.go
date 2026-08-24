package importer

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type ProviderFormat string

const (
	FormatCodexJSONL  ProviderFormat = "codex-jsonl"
	FormatClaudeJSONL ProviderFormat = "claude-jsonl"
)

// HistoryAdapter is the provider boundary. Implementations normalize only
// externally visible conversation and evidence; proprietary hidden state must
// never be returned in SessionTranscript.
type HistoryAdapter interface {
	Format() ProviderFormat
	Decode(data []byte) (SessionTranscript, error)
}

type CodexJSONLAdapter struct{}

func (CodexJSONLAdapter) Format() ProviderFormat { return FormatCodexJSONL }
func (CodexJSONLAdapter) Decode(data []byte) (SessionTranscript, error) {
	return decodeCodexJSONL(data)
}

type ClaudeJSONLAdapter struct{}

func (ClaudeJSONLAdapter) Format() ProviderFormat { return FormatClaudeJSONL }
func (ClaudeJSONLAdapter) Decode(data []byte) (SessionTranscript, error) {
	return decodeClaudeJSONL(data)
}

const (
	maxHistoryBytes = 16 << 20
	maxJSONLLine    = 1 << 20
)

// ImportProviderHistory converts a verified provider-native history format to
// the model-neutral interchange representation before applying the common
// secret firewall and candidate-only policy.
func (s *SessionImporter) ImportProviderHistory(ctx context.Context, projectID string, format ProviderFormat, data []byte, dryRun bool) (ImportResult, error) {
	if err := ctx.Err(); err != nil {
		return ImportResult{}, err
	}
	if len(data) == 0 {
		return ImportResult{}, errors.New("empty transcript payload")
	}
	if len(data) > maxHistoryBytes {
		return ImportResult{}, fmt.Errorf("provider history exceeds %d-byte limit", maxHistoryBytes)
	}

	adapter, ok := s.adapters[format]
	if !ok {
		return ImportResult{}, fmt.Errorf("unsupported provider history format %q", format)
	}
	tr, err := adapter.Decode(data)
	if err != nil {
		return ImportResult{}, err
	}
	return s.importTranscript(ctx, projectID, tr, string(format), dryRun)
}

func scanJSONL(data []byte, visit func([]byte) error) error {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64*1024), maxJSONLLine)
	line := 0
	for scanner.Scan() {
		line++
		if len(bytes.TrimSpace(scanner.Bytes())) == 0 {
			continue
		}
		if err := visit(scanner.Bytes()); err != nil {
			return fmt.Errorf("decode JSONL line %d: %w", line, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan provider history: %w", err)
	}
	return nil
}

type codexEnvelope struct {
	Timestamp time.Time       `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type codexMeta struct {
	ID            string    `json:"id"`
	SessionID     string    `json:"session_id"`
	ModelProvider string    `json:"model_provider"`
	CWD           string    `json:"cwd"`
	Timestamp     time.Time `json:"timestamp"`
}

type codexResponse struct {
	Type    string `json:"type"`
	Role    string `json:"role"`
	Phase   string `json:"phase"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

func decodeCodexJSONL(data []byte) (SessionTranscript, error) {
	tr := SessionTranscript{Provider: "codex"}
	err := scanJSONL(data, func(line []byte) error {
		var envelope codexEnvelope
		if err := json.Unmarshal(line, &envelope); err != nil {
			return err
		}
		switch envelope.Type {
		case "session_meta":
			var meta codexMeta
			if err := json.Unmarshal(envelope.Payload, &meta); err != nil {
				return err
			}
			sessionID := firstNonEmpty(meta.SessionID, meta.ID)
			if tr.SessionID != "" && sessionID != "" && tr.SessionID != sessionID {
				return errors.New("Codex history contains multiple session IDs")
			}
			tr.SessionID = firstNonEmpty(tr.SessionID, sessionID)
			tr.Provider = firstNonEmpty(meta.ModelProvider, tr.Provider)
			tr.CWD = meta.CWD
			tr.Timestamp = firstTime(meta.Timestamp, envelope.Timestamp, tr.Timestamp)
		case "response_item":
			var item codexResponse
			if err := json.Unmarshal(envelope.Payload, &item); err != nil {
				return err
			}
			if item.Type != "message" || (item.Role != "user" && item.Role != "assistant") {
				return nil
			}
			// Only final answers are durable evidence. Commentary can contain
			// incomplete reasoning and must not be imported as memory.
			if item.Role == "assistant" && item.Phase != "final_answer" {
				return nil
			}
			wantType := "input_text"
			if item.Role == "assistant" {
				wantType = "output_text"
			}
			for _, content := range item.Content {
				if content.Type == wantType && strings.TrimSpace(content.Text) != "" {
					tr.Messages = append(tr.Messages, Message{Role: item.Role, Content: content.Text})
				}
			}
		}
		return nil
	})
	if err != nil {
		return SessionTranscript{}, fmt.Errorf("parse Codex history: %w", err)
	}
	return tr, nil
}

type claudeEntry struct {
	Type      string    `json:"type"`
	SessionID string    `json:"sessionId"`
	Timestamp time.Time `json:"timestamp"`
	CWD       string    `json:"cwd"`
	GitBranch string    `json:"gitBranch"`
	Message   struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

func decodeClaudeJSONL(data []byte) (SessionTranscript, error) {
	tr := SessionTranscript{Provider: "claude"}
	err := scanJSONL(data, func(line []byte) error {
		var entry claudeEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			return err
		}
		if entry.Type != "user" && entry.Type != "assistant" {
			return nil
		}
		if tr.SessionID != "" && entry.SessionID != "" && tr.SessionID != entry.SessionID {
			return errors.New("Claude history contains multiple session IDs")
		}
		tr.SessionID = firstNonEmpty(tr.SessionID, entry.SessionID)
		tr.CWD = firstNonEmpty(tr.CWD, entry.CWD)
		tr.Branch = firstNonEmpty(tr.Branch, entry.GitBranch)
		tr.Timestamp = firstTime(tr.Timestamp, entry.Timestamp)

		// The outer event type is the provider's authoritative role marker. Do
		// not allow nested content to relabel an event across trust boundaries.
		role := entry.Type
		var plain string
		if err := json.Unmarshal(entry.Message.Content, &plain); err == nil {
			if strings.TrimSpace(plain) != "" {
				tr.Messages = append(tr.Messages, Message{Role: role, Content: plain})
			}
			return nil
		}
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(entry.Message.Content, &blocks); err != nil {
			return fmt.Errorf("parse Claude message content: %w", err)
		}
		for _, block := range blocks {
			// Exclude thinking, tool_use and tool_result blocks by construction.
			if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
				tr.Messages = append(tr.Messages, Message{Role: role, Content: block.Text})
			}
		}
		return nil
	})
	if err != nil {
		return SessionTranscript{}, fmt.Errorf("parse Claude history: %w", err)
	}
	return tr, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func firstTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}
