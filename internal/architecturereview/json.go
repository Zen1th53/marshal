package architecturereview

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Zen1th53/marshal/internal/architecturecodec"
	"io"
	"regexp"
	"strings"
)

const Version = "architecture-review.v1"

var identifier = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
var requestIDPattern = regexp.MustCompile(`^[0-9a-f]{24}$`)
var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func decodeJSON(raw []byte) (any, error) {
	if len(raw) > 1_000_000 {
		return nil, errors.New("architecture JSON exceeds size limit")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	value, err := readJSON(d, 0)
	if err != nil {
		return nil, err
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, errors.New("architecture JSON has trailing data")
	}
	return value, nil
}
func readJSON(d *json.Decoder, depth int) (any, error) {
	if depth > 64 {
		return nil, errors.New("architecture JSON exceeds depth limit")
	}
	token, err := d.Token()
	if err != nil {
		return nil, err
	}
	if delimiter, ok := token.(json.Delim); ok {
		switch delimiter {
		case '{':
			object := map[string]any{}
			for d.More() {
				keyToken, err := d.Token()
				if err != nil {
					return nil, err
				}
				key, ok := keyToken.(string)
				if !ok {
					return nil, errors.New("invalid JSON object key")
				}
				if _, exists := object[key]; exists {
					return nil, fmt.Errorf("duplicate JSON field %q", key)
				}
				value, err := readJSON(d, depth+1)
				if err != nil {
					return nil, err
				}
				object[key] = value
			}
			_, err := d.Token()
			return object, err
		case '[':
			values := []any{}
			for d.More() {
				value, err := readJSON(d, depth+1)
				if err != nil {
					return nil, err
				}
				values = append(values, value)
			}
			_, err := d.Token()
			return values, err
		default:
			return nil, errors.New("invalid JSON delimiter")
		}
	}
	return token, nil
}
func decodeObject(raw []byte) (map[string]any, error) {
	value, err := decodeJSON(raw)
	if err != nil {
		return nil, err
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("architecture value must be an object")
	}
	return object, nil
}
func canonicalJSON(value any) (json.RawMessage, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	// Normalize nested structs and maps through the same typed JSON tree;
	// geometry supplied as a struct must match geometry read from its journal.
	normalized, err := decodeJSON(buffer.Bytes())
	if err != nil {
		return nil, err
	}
	buffer.Reset()
	if err := encoder.Encode(normalized); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte("\n")), nil
}

// CanonicalDigest uses sorted object keys, preserves JSON number spellings,
// distinguishes bools/numbers, and rejects duplicate fields before hashing.
// HTML escaping is disabled; standard Go JSON escapes U+2028 and U+2029.
func CanonicalDigest(raw json.RawMessage) (string, error) {
	value, err := decodeJSON(raw)
	if err != nil {
		return "", err
	}
	data, err := canonicalJSON(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
func digestValue(value any) (string, error) {
	raw, err := canonicalJSON(value)
	if err != nil {
		return "", err
	}
	return CanonicalDigest(raw)
}
func equalJSON(left, right any) bool {
	a, err := canonicalJSON(left)
	if err != nil {
		return false
	}
	b, err := canonicalJSON(right)
	return err == nil && bytes.Equal(a, b)
}
func cloneObject(object map[string]any) (map[string]any, error) {
	raw, err := canonicalJSON(object)
	if err != nil {
		return nil, err
	}
	return decodeObject(raw)
}
func onlyKeys(object map[string]any, allowed ...string) bool {
	set := map[string]bool{}
	for _, key := range allowed {
		set[key] = true
	}
	for key := range object {
		if !set[key] {
			return false
		}
	}
	return true
}
func nonempty(value any) bool { text, ok := value.(string); return ok && strings.TrimSpace(text) != "" }
func integer(value any) (int64, bool) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	n, err := number.Int64()
	return n, err == nil
}

func validateArchitecture(a map[string]any) error {
	if !nonempty(a["title"]) || !nonempty(a["purpose"]) {
		return errors.New("architecture title or purpose is missing")
	}
	components, ok := a["components"].([]any)
	if !ok || len(components) == 0 {
		return errors.New("architecture components are missing")
	}
	ids := map[string]bool{}
	for _, value := range components {
		c, ok := value.(map[string]any)
		if !ok {
			return errors.New("invalid architecture component")
		}
		id, ok := c["id"].(string)
		if !ok || !identifier.MatchString(id) || ids[id] {
			return errors.New("architecture component IDs must be exact and unique")
		}
		for _, key := range []string{"name", "responsibility", "technology"} {
			if _, ok := c[key].(string); !ok {
				return errors.New("component description must be a string")
			}
		}
		ids[id] = true
	}
	connections, ok := a["connections"].([]any)
	if !ok {
		return errors.New("architecture connections must be an array")
	}
	edges := map[string]bool{}
	for _, value := range connections {
		e, ok := value.(map[string]any)
		if !ok {
			return errors.New("invalid architecture connection")
		}
		id, ok := e["id"].(string)
		source, sourceOK := e["source"].(string)
		target, targetOK := e["target"].(string)
		if !ok || !identifier.MatchString(id) || edges[id] || !sourceOK || !targetOK || !ids[source] || !ids[target] {
			return errors.New("architecture connection IDs or endpoints are invalid")
		}
		for _, key := range []string{"label", "protocol"} {
			if _, ok := e[key].(string); !ok {
				return errors.New("connection description must be a string")
			}
		}
		edges[id] = true
	}
	return nil
}

func ValidateArchitecture(raw json.RawMessage) error {
	architecture, err := decodeObject(raw)
	if err != nil {
		return err
	}
	return validateArchitecture(architecture)
}

// ValidateStoredState returns architecture data without private feedback text.
// Human acceptance is reported only for an exactly bound operator receipt.
func ValidateStoredState(raw json.RawMessage, project string) (json.RawMessage, error) {
	s, err := decodeObject(raw)
	if err != nil {
		return nil, err
	}
	if s["protocol_version"] != Version || s["project"] != project || !nonempty(s["architecture_id"]) {
		return nil, errors.New("architecture review is not bound to this project")
	}
	a, ok := s["architecture"].(map[string]any)
	if !ok {
		return nil, errors.New("stored architecture is invalid")
	}
	if err = validateArchitecture(a); err != nil {
		return nil, err
	}
	digest, err := digestValue(a)
	if err != nil {
		return nil, err
	}
	revision, ok := integer(s["revision"])
	if !ok || revision < 1 || s["digest"] != digest {
		return nil, errors.New("accepted architecture digest or revision is stale")
	}
	approved, ok := s["approved"].(bool)
	if !ok {
		return nil, errors.New("architecture approval status is invalid")
	}
	language, validLanguage := s["language"].(string)
	ui, validUI := s["ui"].(map[string]any)
	if !validLanguage || strings.TrimSpace(language) == "" || !validUI {
		return nil, errors.New("architecture display language binding is missing")
	}
	if err = validateNormalizedUI(ui, language, a); err != nil {
		return nil, err
	}
	uiDigest, err := digestValue(ui)
	if err != nil || s["ui_digest"] != uiDigest || s["ui_architecture_digest"] != digest {
		return nil, errors.New("architecture display translation digest is stale")
	}
	status := "draft"
	comments, ok := s["comments"].([]any)
	if !ok {
		return nil, errors.New("architecture request history is invalid")
	}
	for _, value := range comments {
		entry, ok := value.(map[string]any)
		if !ok {
			return nil, errors.New("architecture feedback entry is invalid")
		}
		resolved, ok := entry["resolved"].(bool)
		if !ok || approved && !resolved {
			return nil, errors.New("architecture still has unresolved feedback")
		}
	}
	if approved {
		approval, ok := s["approval"].(map[string]any)
		if !ok || !architecturecodec.ReviewApprovalOriginAllowed(approval) || approval["architecture_id"] != s["architecture_id"] || approval["digest"] != digest || !equalJSON(approval["revision"], s["revision"]) {
			return nil, errors.New("architecture acceptance receipt does not match")
		}
		layoutDigest, valid := s["layout_digest"].(string)
		if !valid || !digestPattern.MatchString(layoutDigest) || approval["layout_digest"] != layoutDigest || approval["view_version"] != "kiss-v1" {
			return nil, errors.New("architecture acceptance drawing is stale")
		}
		if approval["language"] != language || approval["ui_digest"] != uiDigest {
			return nil, errors.New("architecture acceptance language or display is stale")
		}
		status = "accepted"
	}
	return canonicalJSON(map[string]any{"status": status, "project": project, "architecture_id": s["architecture_id"], "revision": s["revision"], "digest": digest, "layout_digest": s["layout_digest"], "architecture": a, "language": language, "ui_digest": uiDigest, "display": displayFor(ui)})
}
