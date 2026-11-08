package app

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"
)

// providerLimitText rewrites provider output for limit detection. Plain text
// lines are kept as they are. A JSON event line (or JSON embedded after a
// text prefix, or a JSON array of events) is replaced by only the text that
// can name a real limit: a rate_limit_event whose status is not "allowed",
// an error result's text, or an error event's message. Informational events
// (init, assistant and user turns, a successful result, a rate_limit_event
// with status "allowed") contribute nothing, so their keys and values (for
// example "rateLimitType") are never read as a limit.
func providerLimitText(text string) string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		out = append(out, limitLineTexts(line)...)
	}
	return strings.Join(out, "\n")
}

func limitLineTexts(line string) []string {
	trimmed := strings.TrimSpace(line)
	i := strings.IndexAny(trimmed, "{[")
	if i < 0 {
		return []string{line}
	}
	rest := trimmed[i:]
	if !looksLikeJSONEvent(rest) {
		return []string{line}
	}
	var texts []string
	if prefix := strings.TrimSpace(trimmed[:i]); prefix != "" {
		texts = append(texts, prefix)
	}
	decoder := json.NewDecoder(strings.NewReader(rest))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil || !isEventValue(value) {
		// A truncated event: read only its error-like string fields.
		return append(texts, fragmentLimitTexts(rest)...)
	}
	texts = append(texts, eventLimitTexts(value)...)
	if tail := strings.TrimSpace(rest[decoder.InputOffset():]); tail != "" {
		texts = append(texts, limitLineTexts(tail)...)
	}
	return texts
}

// looksLikeJSONEvent reports text that starts a JSON object or an array of
// objects, so "socat[4] W exiting" stays plain text.
func looksLikeJSONEvent(text string) bool {
	text = strings.TrimLeft(text, "[ \t")
	return strings.HasPrefix(text, `{"`) || strings.HasPrefix(text, "{}") || strings.HasPrefix(text, "{ ")
}

func isEventValue(value any) bool {
	switch v := value.(type) {
	case map[string]any:
		return true
	case []any:
		for _, item := range v {
			if _, ok := item.(map[string]any); !ok {
				return false
			}
		}
		return len(v) > 0
	}
	return false
}

// informationalEventTypes never name a limit: their text is the model's own
// turn or session metadata.
var informationalEventTypes = map[string]bool{
	"system": true, "assistant": true, "user": true, "stream_event": true,
	"tool_use": true, "tool_result": true, "text": true, "reasoning": true,
	"step_start": true, "step_finish": true,
}

// modelContentKeys hold model or tool content in generic events, not a
// provider's own error text.
var modelContentKeys = map[string]bool{
	"text": true, "content": true, "delta": true, "thinking": true, "prompt": true,
	"input": true, "arguments": true, "command": true, "aggregated_output": true, "output": true,
}

func eventLimitTexts(value any) []string {
	switch v := value.(type) {
	case []any:
		var texts []string
		for _, item := range v {
			texts = append(texts, eventLimitTexts(item)...)
		}
		return texts
	case map[string]any:
		kind, _ := v["type"].(string)
		switch {
		case kind == "rate_limit_event":
			return rateLimitEventTexts(v)
		case kind == "result":
			return resultEventTexts(v)
		case informationalEventTypes[kind]:
			return nil
		}
		var texts []string
		collectLimitStrings(v, &texts)
		return texts
	}
	return nil
}

// rateLimitEventTexts names a Claude rate_limit_event only when the provider
// refused the request; "allowed" and "allowed_warning" are informational.
func rateLimitEventTexts(event map[string]any) []string {
	info, _ := event["rate_limit_info"].(map[string]any)
	status, _ := info["status"].(string)
	status = strings.ToLower(strings.TrimSpace(status))
	if status == "" || status == "allowed" || status == "allowed_warning" {
		return nil
	}
	text := "rate limit " + status
	if kind, _ := info["rateLimitType"].(string); kind != "" {
		text += " (" + kind + ")"
	}
	if resets, ok := info["resetsAt"].(json.Number); ok {
		if seconds, err := resets.Int64(); err == nil && seconds > 0 {
			text += ", resets at " + time.Unix(seconds, 0).UTC().Format(time.RFC3339)
		}
	}
	return []string{text}
}

// resultEventTexts reads a result event only when it reports an error; a
// successful result's text is the model's reply.
func resultEventTexts(event map[string]any) []string {
	isError, _ := event["is_error"].(bool)
	subtype, _ := event["subtype"].(string)
	if !isError && (subtype == "" || subtype == "success") {
		return nil
	}
	var texts []string
	if result, ok := event["result"].(string); ok && strings.TrimSpace(result) != "" {
		texts = append(texts, boundLimitText(result))
	}
	for _, key := range []string{"error", "errors"} {
		if value, ok := event[key]; ok {
			collectLimitStrings(value, &texts)
		}
	}
	if status, ok := event["api_error_status"].(json.Number); ok && status.String() == "429" {
		texts = append(texts, "HTTP 429 too many requests")
	}
	return texts
}

func collectLimitStrings(value any, texts *[]string) {
	switch v := value.(type) {
	case string:
		if strings.TrimSpace(v) != "" {
			*texts = append(*texts, boundLimitText(v))
		}
	case []any:
		for _, item := range v {
			collectLimitStrings(item, texts)
		}
	case map[string]any:
		if kind, _ := v["type"].(string); kind != "" && informationalEventTypes[kind] {
			return
		}
		for key, item := range v {
			if modelContentKeys[key] {
				continue
			}
			collectLimitStrings(item, texts)
		}
	}
}

var rxJSONErrorField = regexp.MustCompile(`"(?:message|error|detail|reason)"\s*:\s*"((?:[^"\\]|\\.)*)"`)
var rxJSONResultField = regexp.MustCompile(`"result"\s*:\s*"((?:[^"\\]|\\.)*)"`)
var rxJSONRejected = regexp.MustCompile(`"rate_limit_info"\s*:\s*\{[^}]*"status"\s*:\s*"rejected"`)

// fragmentLimitTexts reads a truncated JSON event: only its error-like
// string fields, an error result's text, and a rejected rate-limit status.
func fragmentLimitTexts(fragment string) []string {
	var texts []string
	for _, match := range rxJSONErrorField.FindAllStringSubmatch(fragment, -1) {
		texts = append(texts, boundLimitText(unquoteJSONFragment(match[1])))
	}
	if strings.Contains(fragment, `"is_error":true`) {
		for _, match := range rxJSONResultField.FindAllStringSubmatch(fragment, -1) {
			texts = append(texts, boundLimitText(unquoteJSONFragment(match[1])))
		}
	}
	if rxJSONRejected.MatchString(fragment) {
		texts = append(texts, "rate limit rejected")
	}
	return texts
}

func unquoteJSONFragment(text string) string {
	var out string
	if json.Unmarshal([]byte(`"`+text+`"`), &out) == nil {
		return out
	}
	return text
}

func boundLimitText(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if len(text) > 300 {
		text = strings.ToValidUTF8(text[:300], "")
	}
	return text
}
