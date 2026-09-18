package importer

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Antigravity's CLI (`agy`) keeps each conversation as its own SQLite database
// of steps, and each step's payload is a protobuf message whose schema is not
// published. This adapter therefore reads the wire format directly and takes
// only the fields observed to carry what a later session needs:
//
//	step type 14   user input           payload 19.2  visible text
//	step type 15   planner response     payload 20.1  visible text
//	                                    payload 20.7  tool call {1 id, 2 name, 3 args JSON}
//	                                    payload 20.3  private reasoning: never read
//	other steps    tool execution       payload 5.4   the call being answered
//	                                    payload 140.2.1 command output
//	                                    payload 31.2  failure text
//	step metadata  1 {1 seconds, 2 nanos}             when the step was created
//
// A field that is absent is skipped rather than guessed at, so a format change
// in a later `agy` release loses evidence instead of inventing it.

// Antigravity step types that carry conversation. Any other step that answers
// a tool call is treated as that call's result.
const (
	antigravityStepUserInput       = 14
	antigravityStepPlannerResponse = 15
)

// AntigravityStep is one row of a conversation database's steps table.
type AntigravityStep struct {
	Index    int
	Type     int
	Metadata []byte
	Payload  []byte
}

// DecodeAntigravityConversation turns an Antigravity CLI conversation into a
// transcript. Visible user and assistant text is always kept; tool calls and
// their results are kept, bounded, when captureTools is set. The model's
// reasoning is excluded by construction: its field is never read.
func DecodeAntigravityConversation(conversationID string, steps []AntigravityStep, captureTools bool) (SessionTranscript, error) {
	if strings.TrimSpace(conversationID) == "" {
		return SessionTranscript{}, errors.New("antigravity conversation has no ID")
	}
	ordered := append([]AntigravityStep(nil), steps...)
	sort.SliceStable(ordered, func(a, b int) bool { return ordered[a].Index < ordered[b].Index })

	tr := SessionTranscript{SessionID: conversationID, Provider: "antigravity"}
	for _, step := range ordered {
		payload, err := parseWire(step.Payload)
		if err != nil {
			// One unreadable step does not make the rest of the conversation
			// unreadable, and it is better lost than misread.
			continue
		}
		at := antigravityStepTime(step)
		if tr.Timestamp.IsZero() && !at.IsZero() {
			tr.Timestamp = at
		}

		switch step.Type {
		case antigravityStepUserInput:
			if text := payload.path(19, 2); strings.TrimSpace(text) != "" {
				tr.Messages = append(tr.Messages, Message{Role: "user", Content: text, Timestamp: at})
			}
		case antigravityStepPlannerResponse:
			response := payload.message(20)
			if response == nil {
				continue
			}
			if text := response.str(1); strings.TrimSpace(text) != "" {
				tr.Messages = append(tr.Messages, Message{Role: "assistant", Content: text, Timestamp: at})
			}
			if !captureTools {
				continue
			}
			for _, call := range response.messages(7) {
				name := call.str(2)
				if name == "" {
					continue
				}
				tr.Messages = append(tr.Messages, Message{
					Role: "assistant", Kind: MessageKindToolUse,
					Content:   renderToolUse(name, antigravityArgs(call.str(3))),
					Timestamp: at,
				})
			}
		default:
			if !captureTools {
				continue
			}
			call := payload.message(5).message(4)
			if call == nil || call.str(2) == "" {
				continue
			}
			output := payload.path(140, 2, 1)
			failure := payload.path(31, 2)
			text, isError := output, false
			if strings.TrimSpace(text) == "" && strings.TrimSpace(failure) != "" {
				text, isError = failure, true
			}
			if rendered := renderToolResult(text, isError); rendered != "" {
				tr.Messages = append(tr.Messages, Message{
					Role: "user", Kind: MessageKindToolResult,
					Content: rendered, Timestamp: at,
				})
			}
		}
	}
	return tr, nil
}

// antigravityStepTime reads the step's creation time from its metadata. A step
// without one keeps a zero time rather than borrowing the clock: a record's
// identity is keyed on it, and it must be the same on every re-import.
func antigravityStepTime(step AntigravityStep) time.Time {
	metadata, err := parseWire(step.Metadata)
	if err != nil {
		return time.Time{}
	}
	created := metadata.message(1)
	if created == nil {
		return time.Time{}
	}
	seconds := created.varint(1)
	if seconds == 0 {
		return time.Time{}
	}
	return time.Unix(int64(seconds), int64(created.varint(2))).UTC()
}

// antigravityArgs passes a call's JSON arguments through when they parse, and
// otherwise wraps the raw text so it is still shown rather than dropped.
func antigravityArgs(raw string) json.RawMessage {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if json.Valid([]byte(raw)) {
		return json.RawMessage(raw)
	}
	encoded, err := json.Marshal(map[string]string{"input": raw})
	if err != nil {
		return nil
	}
	return encoded
}

// wireMessage is a protobuf message read without a schema: each field number
// maps to the values that appeared under it, in order.
type wireMessage map[int][]wireValue

type wireValue struct {
	bytes  []byte
	number uint64
	isLen  bool
}

// maxWireDepth bounds recursion through nested messages. The fields this
// adapter reads are at most three levels deep.
const maxWireDepth = 8

func parseWire(data []byte) (wireMessage, error) {
	message := wireMessage{}
	for i := 0; i < len(data); {
		key, n := readVarint(data[i:])
		if n == 0 {
			return nil, errors.New("truncated field key")
		}
		i += n
		field, wireType := int(key>>3), key&7
		if field == 0 {
			return nil, errors.New("field number zero")
		}
		switch wireType {
		case 0:
			value, n := readVarint(data[i:])
			if n == 0 {
				return nil, errors.New("truncated varint")
			}
			i += n
			message[field] = append(message[field], wireValue{number: value})
		case 1:
			if i+8 > len(data) {
				return nil, errors.New("truncated fixed64")
			}
			i += 8
		case 5:
			if i+4 > len(data) {
				return nil, errors.New("truncated fixed32")
			}
			i += 4
		case 2:
			length, n := readVarint(data[i:])
			if n == 0 || length > uint64(len(data)-i-n) {
				return nil, errors.New("truncated length-delimited field")
			}
			i += n
			message[field] = append(message[field], wireValue{bytes: data[i : i+int(length)], isLen: true})
			i += int(length)
		default:
			return nil, fmt.Errorf("unsupported wire type %d", wireType)
		}
	}
	return message, nil
}

func readVarint(data []byte) (uint64, int) {
	var value uint64
	for i := 0; i < len(data) && i < 10; i++ {
		value |= uint64(data[i]&0x7f) << (7 * i)
		if data[i] < 0x80 {
			return value, i + 1
		}
	}
	return 0, 0
}

// message returns the first occurrence of field parsed as a nested message.
func (m wireMessage) message(field int) wireMessage {
	messages := m.messages(field)
	if len(messages) == 0 {
		return nil
	}
	return messages[0]
}

// messages returns every occurrence of field that parses as a message.
func (m wireMessage) messages(field int) []wireMessage {
	if m == nil {
		return nil
	}
	var out []wireMessage
	for _, value := range m[field] {
		if !value.isLen {
			continue
		}
		nested, err := parseWire(value.bytes)
		if err != nil {
			continue
		}
		out = append(out, nested)
	}
	return out
}

// str returns the first occurrence of field as text.
func (m wireMessage) str(field int) string {
	if m == nil {
		return ""
	}
	for _, value := range m[field] {
		if value.isLen {
			return string(value.bytes)
		}
	}
	return ""
}

func (m wireMessage) varint(field int) uint64 {
	if m == nil {
		return 0
	}
	for _, value := range m[field] {
		if !value.isLen {
			return value.number
		}
	}
	return 0
}

// path follows nested messages and returns the final field as text.
func (m wireMessage) path(fields ...int) string {
	if len(fields) == 0 || len(fields) > maxWireDepth {
		return ""
	}
	current := m
	for _, field := range fields[:len(fields)-1] {
		current = current.message(field)
		if current == nil {
			return ""
		}
	}
	return current.str(fields[len(fields)-1])
}
