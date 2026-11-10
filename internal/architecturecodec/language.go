package architecturecodec

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// EnglishUIStrings is the code-owned fallback. Approved languages are supplied
// by validated model translations, rather than a second hardcoded text table.
func EnglishUIStrings() map[string]string {
	return map[string]string{
		"architecture": "Architecture", "traffic": "Traffic", "marshal": "Marshal", "diagram": "Diagram",
		"select_area": "Select area", "approve": "Accept architecture", "approved": "✓ Accepted", "working": "Marshal is working…", "fit": "Fit",
		"view_hint": "Click a part to open it · Use a pencil to comment", "area_hint": "Drag to select an area", "request_type": "Request type",
		"comment": "Comment", "change": "Change request", "request_label": "Write to Marshal", "placeholder": "Comment or request a precise change here…",
		"submit": "Submit to Marshal", "withdraw": "Withdraw request", "close": "Close", "pencil_prefix": "Comment:", "version": "Revision",
		"selected_area": "Selected area", "empty_area": "This area contains no architecture elements.", "connection": "Connection",
		"request_failed": "Request failed", "waiting": "Waiting for Marshal…", "discard_prompt": "There is an unsent request. Close this panel?",
		"request_empty": "Write a comment or request first.", "stale_selection": "The diagram changed. Select the target again.",
		"submitted": "Submitted to Marshal.", "pending_text": "There is an unsent request.", "approved_notice": "Accepted — the run continues in your terminal.",
		"updated_notice": "Marshal prepared a new proposal. Select again for a new request.", "withdrawn": "Request withdrawn; history retained.",
		"what_it_does": "What it does", "files": "Files", "no_files": "No files listed yet.", "builder": "Builder",
		"builder_auto": "Auto (Marshal picks)", "builder_unavailable": "unavailable", "builder_saved": "Builder saved.",
		"builder_locked": "Accepted — builders are fixed for this run.", "comment_on_part": "Comment on this part",
		"accept_summary": "{parts} parts · {builders} builders chosen · {open} open comments", "accept_summary_plain": "{parts} parts · {open} open comments",
		"accept_wait_reply": "Wait for the Marshal to answer your request.", "accept_open_request": "Answer or withdraw the open request first.",
		"accept_unsent": "Send or clear the text you typed first.", "accepted_banner": "Accepted — the run continues in your terminal.",
		"reopen_prompt": "This architecture is accepted. Sending this request reopens it and pauses the run until you accept again. Send it?",
		"revision_first": "rev {rev} · first draft", "revision_changed": "rev {rev} · {n} parts changed since rev {prev}",
		"revision_changed_one": "rev {rev} · 1 part changed since rev {prev}", "changed_badge": "changed", "design": "Design",
		"design_brief": "Brief", "design_findings": "Review by {provider}", "design_resolutions": "How each finding was resolved",
		"design_no_resolutions": "No resolutions recorded yet.", "design_shortened": "(shortened)", "accept_with_design": "design included",
	}
}

// legacyUIStringKeys is the interface label set that existed before the browser
// v2 keys were added. A stored translation covering exactly these keys stays
// valid; the page falls back to English for the newer keys.
var legacyUIStringKeys = []string{
	"architecture", "traffic", "marshal", "diagram", "select_area", "approve", "approved", "working", "fit",
	"view_hint", "area_hint", "request_type", "comment", "change", "request_label", "placeholder",
	"submit", "withdraw", "close", "pencil_prefix", "version", "selected_area", "empty_area", "connection",
	"request_failed", "waiting", "discard_prompt", "request_empty", "stale_selection", "submitted",
	"pending_text", "approved_notice", "updated_notice", "withdrawn",
}

func approvedLanguage(language string) string {
	language = strings.TrimSpace(language)
	if language == "" {
		return "English"
	}
	return language
}

// ValidateUI binds a translation to the approved language and exact canonical
// IDs. It returns the dictionary overlay consumed by the browser.
func ValidateUI(raw json.RawMessage, language string, proposal json.RawMessage) (json.RawMessage, error) {
	language = approvedLanguage(language)
	ui, err := decodeObject(raw)
	if err != nil {
		return nil, err
	}
	a, err := decodeObject(proposal)
	if err != nil {
		return nil, err
	}
	if err = validateArchitecture(a); err != nil {
		return nil, err
	}
	if !onlyKeys(ui, "language", "strings", "components", "connections") || len(ui) != 4 || ui["language"] != language {
		return nil, errors.New("UI translation language or fields differ from the approved request")
	}
	result := map[string]any{"language": language}
	for _, collection := range []string{"strings", "components", "connections"} {
		items, ok := ui[collection].([]any)
		if !ok {
			return nil, fmt.Errorf("UI translation %s must be an array", collection)
		}
		dict := map[string]any{}
		for _, value := range items {
			item, ok := value.(map[string]any)
			if !ok {
				return nil, errors.New("invalid UI translation entry")
			}
			keyField, allowed := "id", []string{"id", "label", "caption"}
			if collection == "strings" {
				keyField = "key"
				allowed = []string{"key", "text"}
			} else if collection == "components" {
				allowed = []string{"id", "name", "responsibility"}
			}
			if !onlyKeys(item, allowed...) || len(item) != len(allowed) {
				return nil, errors.New("UI translation cannot supply topology or unknown fields")
			}
			key, ok := item[keyField].(string)
			if !ok || key == "" {
				return nil, errors.New("UI translation identity is missing")
			}
			if _, exists := dict[key]; exists {
				return nil, errors.New("UI translation target is duplicated")
			}
			if collection == "strings" {
				dict[key] = item["text"]
			} else {
				translated := map[string]any{}
				for field, value := range item {
					if field != "id" {
						translated[field] = value
					}
				}
				dict[key] = translated
			}
		}
		result[collection] = dict
	}
	if err = validateNormalizedUI(result, language, a); err != nil {
		return nil, err
	}
	english := EnglishUIStrings()
	translated := result["strings"].(map[string]any)
	if len(translated) != len(english) {
		return nil, errors.New("UI translation does not cover every interface label")
	}
	for key, englishText := range english {
		text, _ := translated[key].(string)
		if !equalPlaceholders(englishText, text) {
			return nil, fmt.Errorf("UI translation label %q must keep its placeholders", key)
		}
	}
	return canonicalJSON(result)
}

var placeholderPattern = regexp.MustCompile(`\{[a-z]+\}`)

// equalPlaceholders reports whether two texts carry the same {placeholder}
// tokens, so a translation cannot drop or invent an interpolated value.
func equalPlaceholders(english, translated string) bool {
	left := placeholderPattern.FindAllString(english, -1)
	right := placeholderPattern.FindAllString(translated, -1)
	sort.Strings(left)
	sort.Strings(right)
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func uiStringKeys() []string {
	expected := EnglishUIStrings()
	keys := make([]string, 0, len(expected))
	for key := range expected {
		keys = append(keys, key)
	}
	return keys
}

func uiStringsCover(texts map[string]any, keys []string) bool {
	if len(texts) != len(keys) {
		return false
	}
	for _, key := range keys {
		if !nonempty(texts[key]) {
			return false
		}
	}
	return true
}

func validateNormalizedUI(ui map[string]any, language string, a map[string]any) error {
	if !onlyKeys(ui, "language", "strings", "components", "connections") || len(ui) != 4 || ui["language"] != language {
		return errors.New("cached UI is not bound to the approved language")
	}
	texts, ok := ui["strings"].(map[string]any)
	if !ok || (!uiStringsCover(texts, uiStringKeys()) && !uiStringsCover(texts, legacyUIStringKeys)) {
		return errors.New("UI translation does not cover every interface label")
	}
	for _, collection := range []string{"components", "connections"} {
		translated, ok := ui[collection].(map[string]any)
		if !ok || len(translated) != len(a[collection].([]any)) {
			return errors.New("UI translation does not match canonical object IDs")
		}
		for _, value := range a[collection].([]any) {
			original := value.(map[string]any)
			id := original["id"].(string)
			item, ok := translated[id].(map[string]any)
			if !ok {
				return errors.New("UI translation names a foreign architecture object")
			}
			if collection == "components" {
				if len(item) != 2 || !onlyKeys(item, "name", "responsibility") || !nonempty(item["name"]) || !nonempty(item["responsibility"]) {
					return errors.New("component display text is invalid")
				}
			} else if len(item) != 2 || !onlyKeys(item, "label", "caption") || !nonempty(item["label"]) || !nonempty(item["caption"]) || utf8.RuneCountInString(item["caption"].(string)) > 28 {
				return errors.New("connection display label or short caption is invalid")
			}
		}
	}
	return nil
}

func englishOverlay(a map[string]any) map[string]any {
	texts := map[string]any{}
	for key, text := range EnglishUIStrings() {
		texts[key] = text
	}
	components, connections := map[string]any{}, map[string]any{}
	for _, value := range a["components"].([]any) {
		c := value.(map[string]any)
		components[c["id"].(string)] = map[string]any{"name": c["name"], "responsibility": c["responsibility"]}
	}
	for _, value := range a["connections"].([]any) {
		e := value.(map[string]any)
		label := e["label"].(string)
		caption := []rune(label)
		if len(caption) > 28 {
			caption = append(caption[:27], '…')
		}
		connections[e["id"].(string)] = map[string]any{"label": label, "caption": string(caption)}
	}
	return map[string]any{"language": "English", "strings": texts, "components": components, "connections": connections}
}

func bindUI(state map[string]any, ui map[string]any) error {
	architectureDigest, err := digestValue(state["architecture"])
	if err != nil {
		return err
	}
	uiDigest, err := digestValue(ui)
	if err != nil {
		return err
	}
	state["language"] = ui["language"]
	state["ui"] = ui
	state["ui_digest"] = uiDigest
	state["ui_architecture_digest"] = architectureDigest
	return nil
}

func displayFor(ui map[string]any) map[string]any {
	return map[string]any{"language": ui["language"], "components": ui["components"], "connections": ui["connections"]}
}
