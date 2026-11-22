package app

import (
	"encoding/json"
	"sort"
	"testing"
)

// Provider CLIs send these schemas in OpenAI's strict structured-output mode,
// which refuses an object whose "required" does not list every property. A
// property missing there makes every turn that uses the schema fail at once
// (seen live: every Codex review stopped the run).
func TestOutputSchemasAreStrict(t *testing.T) {
	schemas := map[string]string{
		"marshalDraftSchema":        marshalDraftSchema,
		"marshalReviewSchema":       marshalReviewSchema,
		"marshalVerifySchema":       marshalVerifySchema,
		"marshalPermissionSchema":   marshalPermissionSchema,
		"marshalArchitectureSchema": marshalArchitectureSchema,
	}
	for name, s := range editionOutputSchemas() {
		schemas[name] = s
	}
	for name, raw := range schemas {
		var v any
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		checkStrictSchema(t, name, "$", v)
	}
}

func checkStrictSchema(t *testing.T, name, at string, v any) {
	t.Helper()
	switch n := v.(type) {
	case map[string]any:
		if props, ok := n["properties"].(map[string]any); ok {
			required := map[string]bool{}
			if list, ok := n["required"].([]any); ok {
				for _, r := range list {
					if s, ok := r.(string); ok {
						required[s] = true
					}
				}
			}
			var missing []string
			for key := range props {
				if !required[key] {
					missing = append(missing, key)
				}
			}
			sort.Strings(missing)
			if len(missing) > 0 {
				t.Errorf("%s %s: properties missing from required: %v", name, at, missing)
			}
			for key, child := range props {
				checkStrictSchema(t, name, at+"."+key, child)
			}
		}
		for _, key := range []string{"items", "anyOf", "oneOf", "allOf", "$defs", "definitions"} {
			if child, ok := n[key]; ok {
				checkStrictSchema(t, name, at+"."+key, child)
			}
		}
	case []any:
		for _, child := range n {
			checkStrictSchema(t, name, at+"[]", child)
		}
	}
}
