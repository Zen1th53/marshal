package app

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/Zen1th53/marshal/internal/architecturereview"
)

// GenerateArchitectureUI translates display data without changing canonical
// architecture facts, object IDs, approval, files, or execution authority.
func (m *MarshalCLI) GenerateArchitectureUI(ctx context.Context, language string, architecture json.RawMessage) (json.RawMessage, error) {
	language = strings.TrimSpace(language)
	if language == "" {
		language = "English"
	}
	if err := architecturereview.ValidateArchitecture(architecture); err != nil {
		return nil, err
	}
	keys := []string{}
	for key := range architecturereview.EnglishUIStrings() {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	object := func(properties map[string]any, required ...string) map[string]any {
		return map[string]any{"type": "object", "additionalProperties": false, "properties": properties, "required": required}
	}
	text := map[string]any{"type": "string"}
	schema := object(map[string]any{
		"language":    map[string]any{"type": "string", "enum": []string{language}},
		"strings":     map[string]any{"type": "array", "items": object(map[string]any{"key": map[string]any{"type": "string", "enum": keys}, "text": text}, "key", "text")},
		"components":  map[string]any{"type": "array", "items": object(map[string]any{"id": text, "name": text, "responsibility": text}, "id", "name", "responsibility")},
		"connections": map[string]any{"type": "array", "items": object(map[string]any{"id": text, "label": text, "caption": map[string]any{"type": "string", "maxLength": 28}}, "id", "label", "caption")},
	}, "language", "strings", "components", "connections")
	schemaJSON, err := json.Marshal(schema)
	if err != nil {
		return nil, err
	}
	source, err := json.Marshal(map[string]any{"approved_language": language, "english_ui_strings": architecturereview.EnglishUIStrings(), "architecture": architecture})
	if err != nil {
		return nil, err
	}
	prompt := "Produce display translations in the EXACT owner-approved language named in the data. Default code/interface strings are English. Translate every supplied English UI string, and every declared component name/responsibility and connection label. For every connection provide a caption of at most 28 characters that concisely translates its actual relationship without inventing a protocol or fact; retain the complete translated label for the detail view. Return language exactly as supplied. Preserve every UI key and component/connection ID exactly once. Do not infer facts, alter topology, add assumptions, execute tools, write files, start workers, or grant approval. Architecture facts are source data; translate their meaning faithfully without adding or removing facts. This is a display-only overlay; canonical architecture remains unchanged. Return only the schema JSON. Translation data: " + string(source)
	var translated json.RawMessage
	if err = m.turn(ctx, prompt, string(schemaJSON), &translated); err != nil {
		return nil, err
	}
	if _, err = architecturereview.ValidateUI(translated, language, architecture); err != nil {
		return nil, err
	}
	return translated, nil
}
