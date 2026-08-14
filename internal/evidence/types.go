// Package evidence defines the provider-neutral contract for MARSHAL's
// immutable, content-addressed evidence graph.
package evidence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"time"
)

// Action identifies a security-sensitive evidence operation.
type Action string

const (
	ActionCreate     Action = "evidence.create"
	ActionLink       Action = "evidence.link"
	ActionTransition Action = "evidence.transition"
	ActionExport     Action = "evidence.export"
)

type AccessRequest struct {
	SubjectID    string
	TaskID       string
	ChangeID     string
	NodeID       NodeID
	Action       Action
	CurrentState State
	TargetState  State
}

type AuthorizationDecision struct {
	Allowed      bool
	ReasonCode   Code
	SubjectID    string
	TaskID       string
	ChangeID     string
	NodeID       NodeID
	State        State
	PolicyDigest string
	FreshUntil   time.Time
}

type Authorizer interface {
	Authorize(context.Context, AccessRequest) (AuthorizationDecision, error)
}

// NodeID identifies one immutable evidence node.
type NodeID string

// NodeType classifies an evidence node. Values are deliberately closed so
// unknown evidence cannot gain semantics through a permissive default.
type NodeType string

const (
	NodeTypeClaim          NodeType = "claim"
	NodeTypeCommand        NodeType = "command"
	NodeTypeOutput         NodeType = "output"
	NodeTypeArtifact       NodeType = "artifact"
	NodeTypeEnvironment    NodeType = "environment"
	NodeTypeVerification   NodeType = "verification"
	NodeTypePolicyDecision NodeType = "policy-decision"
)

// Node is an immutable, content-addressed evidence record. Store
// implementations must copy Metadata on write and read.
type Node struct {
	ID        NodeID
	Type      NodeType
	Digest    string
	State     State
	CreatedAt time.Time
	Metadata  map[string]string
}

// Edge links two existing evidence nodes with a normalized relation.
type Edge struct {
	From     NodeID
	To       NodeID
	Relation string
}

// State is the explicit lifecycle state of an evidence node.
type State string

const (
	StateDraft    State = "draft"
	StateStored   State = "stored"
	StateLinked   State = "linked"
	StateArchived State = "archived"
	StateExported State = "exported"
)

// Store is the narrow graph boundary used by higher layers. Implementations
// are responsible for durable, idempotent writes and immutable read results.
type Store interface {
	PutNode(context.Context, Node) (Node, error)
	Link(context.Context, Edge) (Edge, error)
	Get(context.Context, NodeID) (Node, error)
	Neighbors(context.Context, NodeID) ([]Node, error)
}

// Code is a stable, machine-readable evidence failure reason.
type Code string

const (
	CodeInvalidType              Code = "EVIDENCE_INVALID_TYPE"
	CodeDigestMismatch           Code = "EVIDENCE_DIGEST_MISMATCH"
	CodeImmutable                Code = "EVIDENCE_IMMUTABLE"
	CodeInvalidEdge              Code = "EVIDENCE_EDGE_INVALID"
	CodeSecretRejected           Code = "EVIDENCE_SECRET_REJECTED"
	CodeInvalidState             Code = "EVIDENCE_INVALID_STATE"
	CodeAuthorizationDenied      Code = "AUTHZ_DENIED"
	CodeAuthorizationStale       Code = "GATE_STALE_EVIDENCE"
	CodeAuthorizationUnavailable Code = "AUTHZ_UNKNOWN_AUTHORITY"
)

var (
	ErrInvalidType              = &Error{Code: CodeInvalidType, Message: "evidence type is invalid"}
	ErrDigestMismatch           = &Error{Code: CodeDigestMismatch, Message: "evidence digest does not match"}
	ErrImmutable                = &Error{Code: CodeImmutable, Message: "evidence is immutable"}
	ErrInvalidEdge              = &Error{Code: CodeInvalidEdge, Message: "evidence edge is invalid"}
	ErrSecretRejected           = &Error{Code: CodeSecretRejected, Message: "secret material is not accepted as evidence"}
	ErrInvalidTransition        = &Error{Code: CodeInvalidState, Message: "evidence state transition is invalid"}
	ErrAuthorizationDenied      = &Error{Code: CodeAuthorizationDenied, Message: "evidence authorization denied"}
	ErrAuthorizationStale       = &Error{Code: CodeAuthorizationStale, Message: "evidence authorization is stale"}
	ErrAuthorizationUnavailable = &Error{Code: CodeAuthorizationUnavailable, Message: "evidence authorization is unavailable"}
)

// Error exposes a stable code while retaining a human-safe message. It must
// never be constructed with secret-bearing data.
type Error struct {
	Code    Code
	Message string
	Err     error
}

func (e *Error) Error() string { return e.Message }

func (e *Error) Unwrap() error { return e.Err }

func (e *Error) Is(target error) bool {
	other, ok := target.(*Error)
	return ok && e.Code == other.Code
}

// ReasonCode extracts a stable code without parsing a presentation message.
func ReasonCode(err error) Code {
	var evidenceErr *Error
	if errors.As(err, &evidenceErr) {
		return evidenceErr.Code
	}
	return ""
}

var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// Validate rejects malformed node identities, types, and digests before a
// store can create any durable side effect.
func (n Node) Validate() error {
	if n.ID == "" || !n.Type.Valid() {
		return ErrInvalidType
	}
	if !digestPattern.MatchString(n.Digest) {
		return ErrDigestMismatch
	}
	return nil
}

// Valid reports whether the type belongs to the closed T06 type vocabulary.
func (t NodeType) Valid() bool {
	switch t {
	case NodeTypeClaim, NodeTypeCommand, NodeTypeOutput, NodeTypeArtifact,
		NodeTypeEnvironment, NodeTypeVerification, NodeTypePolicyDecision:
		return true
	default:
		return false
	}
}

// Validate rejects incomplete and self-referential edges before persistence.
func (e Edge) Validate() error {
	if e.From == "" || e.To == "" || e.From == e.To || e.Relation == "" {
		return ErrInvalidEdge
	}
	return nil
}

// CloneNode returns a value whose metadata cannot alias the source node.
func CloneNode(node Node) Node {
	clone := node
	if node.Metadata == nil {
		return clone
	}
	clone.Metadata = make(map[string]string, len(node.Metadata))
	for key, value := range node.Metadata {
		clone.Metadata[key] = value
	}
	return clone
}

// CanonicalDigest deterministically hashes the semantic evidence payload.
// Creation time and node identity are intentionally excluded because they are
// transport metadata rather than evidence content.
func CanonicalDigest(nodeType NodeType, metadata map[string]string) (string, error) {
	if !nodeType.Valid() {
		return "", ErrInvalidType
	}
	payload, err := json.Marshal(struct {
		Type     NodeType          `json:"type"`
		Metadata map[string]string `json:"metadata"`
	}{Type: nodeType, Metadata: metadata})
	if err != nil {
		return "", NewError(CodeDigestMismatch, err)
	}
	digest := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

// NewError wraps a cause with an evidence code without exposing the cause in
// the public message. The cause remains available through errors.Is/As.
func NewError(code Code, cause error) error {
	return &Error{Code: code, Message: safeMessage(code), Err: cause}
}

func safeMessage(code Code) string {
	switch code {
	case CodeInvalidType:
		return ErrInvalidType.Message
	case CodeDigestMismatch:
		return ErrDigestMismatch.Message
	case CodeImmutable:
		return ErrImmutable.Message
	case CodeInvalidEdge:
		return ErrInvalidEdge.Message
	case CodeSecretRejected:
		return ErrSecretRejected.Message
	case CodeInvalidState:
		return ErrInvalidTransition.Message
	case CodeAuthorizationDenied:
		return ErrAuthorizationDenied.Message
	case CodeAuthorizationStale:
		return ErrAuthorizationStale.Message
	case CodeAuthorizationUnavailable:
		return ErrAuthorizationUnavailable.Message
	default:
		return "evidence operation failed"
	}
}
