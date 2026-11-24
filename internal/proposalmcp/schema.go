package proposalmcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// The inner payloads of marshal_proposal_submit and
// marshal_project_contract_submit are described by one table each. The same
// table produces the JSON Schema shown in tools/list and the shape check run
// before any authority, so the described and the enforced shapes cannot drift.
// The check is typing only: authorization, leases, state and byte limits stay
// with the runtime that receives the proposal.

// node is one value in a payload shape.
type node struct {
	typ       string // "object", "array", "string", "integer", "boolean"
	props     map[string]*node
	required  []string
	items     *node
	enum      []string
	maxLength int
	maxItems  int
	minimum   *int64
	maximum   *int64
}

func str() *node             { return &node{typ: "string"} }
func strMax(n int) *node     { return &node{typ: "string", maxLength: n} }
func boolean() *node         { return &node{typ: "boolean"} }
func enum(v ...string) *node { return &node{typ: "string", enum: v} }
func strs() *node            { return &node{typ: "array", items: str()} }
func arr(item *node, max int) *node {
	return &node{typ: "array", items: item, maxItems: max}
}
func integer(min, max *int64) *node { return &node{typ: "integer", minimum: min, maximum: max} }
func obj(props map[string]*node, required ...string) *node {
	return &node{typ: "object", props: props, required: required}
}
func i64(v int64) *int64 { return &v }

// LiveProposalActions are the actions marshal_proposal_submit accepts. decide
// answers an open chat question and exists only on the live channel; every
// other action goes through the same parser as a proposal file.
var LiveProposalActions = []string{"decide", "intake", "ask", "requirements", "hold", "release", "setting", "continue", "read", "memory", "accept", "return", "approve", "amend-approve", "amend-deny", "close", "resume", "deliver", "amend", "architecture", "retry", "reassign", "harness-verify", "goal", "approve-task"}

// SettingKeys are the setting proposal keys.
var SettingKeys = []string{"acceptance-mode", "execution-rights", "control", "rework-limit", "ultra-concurrency", "task-tokens", "plan-tokens", "task-money", "plan-money", "task-wall-seconds", "plan-wall-seconds"}

// proposalFields types every proposal key. Apart from allow and
// fresh_session, every value is a JSON string.
var proposalFields = map[string]*node{
	"action":        enum(LiveProposalActions...),
	"id":            strMax(200),
	"allow":         boolean(),
	"reason":        strMax(500),
	"language":      strMax(100),
	"question":      strMax(300),
	"choices":       str(),
	"card":          str(),
	"ids":           str(),
	"until":         enum("release"),
	"key":           enum(SettingKeys...),
	"value":         strMax(2000),
	"path":          str(),
	"provider":      strMax(100),
	"model":         strMax(200),
	"task_id":       strMax(200),
	"target_worker": strMax(100),
	"fresh_session": boolean(),
	"criteria":      str(),
}

// actionKeys lists each action's keys besides action; a trailing "?" marks an
// optional key.
var actionKeys = map[string][]string{
	"decide":         {"id", "allow", "reason"},
	"intake":         {"language"},
	"ask":            {"question", "choices"},
	"requirements":   {"card"},
	"hold":           {"ids", "reason", "until?"},
	"release":        {"ids"},
	"setting":        {"key", "value"},
	"continue":       {"path", "provider"},
	"read":           {"path"},
	"memory":         {"id"},
	"accept":         {"id"},
	"return":         {"id", "reason"},
	"approve":        {},
	"amend-approve":  {},
	"amend-deny":     {},
	"close":          {},
	"resume":         {},
	"deliver":        {},
	"amend":          {"reason"},
	"architecture":   {"reason"},
	"retry":          {"task_id", "fresh_session"},
	"reassign":       {"task_id", "target_worker"},
	"harness-verify": {"provider", "model?"},
	"goal":           {"value", "criteria?"},
	"approve-task":   {"id"},
}

// ProposalActionKeys returns an action's required and optional keys
// (action excluded); ok is false for an unknown action.
func ProposalActionKeys(action string) (required, optional []string, ok bool) {
	keys, ok := actionKeys[action]
	for _, k := range keys {
		if name, opt := strings.CutSuffix(k, "?"); opt {
			optional = append(optional, name)
		} else {
			required = append(required, k)
		}
	}
	return required, optional, ok
}

// ProposalSchema is the JSON Schema of the marshal_proposal_submit proposal.
func ProposalSchema() map[string]any {
	props := map[string]any{}
	for k, v := range proposalFields {
		props[k] = v.schema()
	}
	// Actions taking the same keys share one branch.
	var branches []any
	index := map[string]int{}
	for _, action := range LiveProposalActions {
		required, optional, _ := ProposalActionKeys(action)
		names := append(append([]string{"action"}, required...), optional...)
		sig := strings.Join(required, ",") + "|" + strings.Join(optional, ",")
		if i, ok := index[sig]; ok {
			b := branches[i].(map[string]any)
			e := b["properties"].(map[string]any)["action"].(map[string]any)
			e["enum"] = append(e["enum"].([]string), action)
			continue
		}
		index[sig] = len(branches)
		b := map[string]any{
			"properties":    map[string]any{"action": map[string]any{"enum": []string{action}}},
			"propertyNames": map[string]any{"enum": names},
		}
		if len(required) > 0 {
			b["required"] = required
		}
		branches = append(branches, b)
	}
	return map[string]any{"type": "object", "properties": props, "required": []string{"action"}, "oneOf": branches}
}

// ValidateProposal checks a proposal's shape for its action and names the
// first offending field.
func ValidateProposal(raw []byte) error {
	v, err := decodeShape(raw)
	if err != nil {
		return err
	}
	m, ok := v.(map[string]any)
	if !ok {
		return fieldError("proposal", "must be a JSON object")
	}
	action, ok := m["action"].(string)
	if !ok {
		return fieldError("proposal.action", "required string, one of "+strings.Join(LiveProposalActions, ", "))
	}
	required, optional, known := ProposalActionKeys(action)
	if !known {
		return fieldError("proposal.action", fmt.Sprintf("unknown action %q; use one of %s", action, strings.Join(LiveProposalActions, ", ")))
	}
	allowed := append(append([]string{"action"}, required...), optional...)
	shape := obj(map[string]*node{}, append([]string{"action"}, required...)...)
	for _, k := range allowed {
		shape.props[k] = proposalFields[k]
	}
	if err := shape.check("proposal", m); err != nil {
		return fmt.Errorf("%w (action %q takes %s)", err, action, strings.Join(allowed, ", "))
	}
	return nil
}

// contractSubmission is the marshal_project_contract_submit proposal: the
// JSON names of the project contract domain types. contract.project may be
// left out; the live runtime binds it to the project it runs in.
var contractSubmission = func() *node {
	memoryRef := obj(map[string]*node{
		"scope": enum("project", "task", "agent", "session", "branch", "team", "operator_private"), "scope_id": str(),
		"id": str(), "project_id": str(), "revision": integer(i64(0), nil),
		"content_digest": str(), "state_fingerprint": str(),
		"class":                 enum("policy", "constraint", "critical_decision", "additional_fact", "context_only"),
		"applies":               obj(map[string]*node{"project_wide": boolean(), "task_ids": strs(), "components": strs(), "paths": strs()}),
		"acceptance_occurrence": str(), "reason": str(),
		"evidence_required": boolean(), "max_verification_age_seconds": integer(i64(0), nil),
	}, "scope", "scope_id", "id", "project_id", "revision", "content_digest", "state_fingerprint", "class", "applies", "acceptance_occurrence", "reason")
	component := obj(map[string]*node{
		"id": str(), "responsibility": str(), "paths": strs(), "dependencies": strs(),
		"parent": str(), "removed": boolean(), "covers": strs(),
	}, "id", "responsibility", "paths")
	contract := obj(map[string]*node{
		"format_version": integer(i64(1), i64(1)),
		"project":        obj(map[string]*node{"canonical_id": str(), "storage_id": str()}, "canonical_id", "storage_id"),
		"id":             str(), "revision": integer(i64(1), nil), "purpose": str(),
		"components":                arr(component, 0),
		"connections":               arr(obj(map[string]*node{"from": str(), "to": str(), "carries": str()}, "from", "to", "carries"), 0),
		"invariants":                strs(),
		"prohibited_changes":        strs(),
		"required":                  arr(memoryRef, 256),
		"reviewed_inventory_digest": str(),
		"source_anchors":            strs(), "decision_refs": strs(),
		"supersedes_digest": str(), "revision_reason": str(),
	}, "format_version", "id", "revision", "purpose", "components", "invariants", "reviewed_inventory_digest")
	return obj(map[string]*node{
		"action":           enum("project-contract"),
		"expected_version": integer(i64(0), nil),
		"contract":         contract,
	}, "action", "expected_version", "contract")
}()

// ContractSubmissionSchema is the JSON Schema of the
// marshal_project_contract_submit proposal.
func ContractSubmissionSchema() map[string]any { return contractSubmission.schema() }

// ValidateContractSubmission checks a contract proposal's shape and names the
// first offending field.
func ValidateContractSubmission(raw []byte) error {
	v, err := decodeShape(raw)
	if err != nil {
		return err
	}
	return contractSubmission.check("proposal", v)
}

func decodeShape(raw []byte) (any, error) {
	if err := rejectDuplicateKeys(raw); err != nil {
		return nil, fieldError("proposal", err.Error())
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fieldError("proposal", "must be valid JSON")
	}
	return v, nil
}

func fieldError(path, msg string) error { return fmt.Errorf("%s: %s", path, msg) }

func (n *node) check(path string, v any) error {
	switch n.typ {
	case "object":
		m, ok := v.(map[string]any)
		if !ok {
			return fieldError(path, "must be an object")
		}
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			if n.props[k] == nil {
				allowed := make([]string, 0, len(n.props))
				for p := range n.props {
					allowed = append(allowed, p)
				}
				slices.Sort(allowed)
				return fieldError(path+"."+k, "unknown field; allowed: "+strings.Join(allowed, ", "))
			}
		}
		for _, k := range n.required {
			if m[k] == nil {
				return fieldError(path+"."+k, "required "+n.props[k].typ)
			}
		}
		for _, k := range keys {
			// An optional field sent as null reads as left out, as the
			// runtime's decoder reads it.
			if m[k] == nil {
				continue
			}
			if err := n.props[k].check(path+"."+k, m[k]); err != nil {
				return err
			}
		}
	case "array":
		a, ok := v.([]any)
		if !ok {
			return fieldError(path, "must be an array")
		}
		if n.maxItems > 0 && len(a) > n.maxItems {
			return fieldError(path, fmt.Sprintf("at most %d items", n.maxItems))
		}
		for i, item := range a {
			if err := n.items.check(fmt.Sprintf("%s[%d]", path, i), item); err != nil {
				return err
			}
		}
	case "string":
		s, ok := v.(string)
		if !ok {
			return fieldError(path, "must be a string")
		}
		if n.maxLength > 0 && len(s) > n.maxLength {
			return fieldError(path, fmt.Sprintf("is %d bytes; the limit is %d", len(s), n.maxLength))
		}
		if len(n.enum) > 0 && !slices.Contains(n.enum, s) {
			return fieldError(path, "must be one of "+strings.Join(n.enum, ", "))
		}
	case "integer":
		num, ok := v.(json.Number)
		if !ok {
			return fieldError(path, "must be an integer")
		}
		i, err := num.Int64()
		if err != nil {
			return fieldError(path, "must be an integer")
		}
		if n.minimum != nil && i < *n.minimum {
			return fieldError(path, fmt.Sprintf("must be at least %d", *n.minimum))
		}
		if n.maximum != nil && i > *n.maximum {
			return fieldError(path, fmt.Sprintf("must be at most %d", *n.maximum))
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return fieldError(path, "must be true or false")
		}
	}
	return nil
}

func (n *node) schema() map[string]any {
	out := map[string]any{"type": n.typ}
	switch n.typ {
	case "object":
		props := map[string]any{}
		for k, v := range n.props {
			props[k] = v.schema()
		}
		out["properties"] = props
		out["additionalProperties"] = false
		if len(n.required) > 0 {
			out["required"] = n.required
		}
	case "array":
		out["items"] = n.items.schema()
		if n.maxItems > 0 {
			out["maxItems"] = n.maxItems
		}
	}
	if len(n.enum) > 0 {
		out["enum"] = n.enum
	}
	if n.maxLength > 0 {
		out["maxLength"] = n.maxLength
	}
	if n.minimum != nil {
		out["minimum"] = *n.minimum
	}
	if n.maximum != nil {
		out["maximum"] = *n.maximum
	}
	return out
}
