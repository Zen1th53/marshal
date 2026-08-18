// Package trustcontent keeps externally sourced context as data in immutable
// transport-assigned trust zones.
package trustcontent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const MaxSegmentBytes = 128 << 10

type Zone string

const (
	System           Zone = "system"
	OwnerPolicy      Zone = "owner_policy"
	ProjectPolicy    Zone = "project_policy"
	TrustedTool      Zone = "trusted_tool"
	RepositoryData   Zone = "repository_data"
	WebData          Zone = "web_data"
	UntrustedContent Zone = "untrusted_content"
)

type Source string

const (
	SourceSystem        Source = "system"
	SourceOwnerPolicy   Source = "owner_policy"
	SourceProjectPolicy Source = "project_policy"
	SourceTrustedTool   Source = "trusted_tool"
	SourceRepository    Source = "repository"
	SourceWeb           Source = "web"
	SourceMCP           Source = "mcp"
	SourceUntrusted     Source = "untrusted"
)

type State string

const (
	StateIngested State = "ingested"
	StateZoned    State = "zoned"
	StateRendered State = "rendered"
)

type ErrorCode string

const (
	CodeZoneInvalid      ErrorCode = "TRUST_ZONE_INVALID"
	CodeUpgradeForbidden ErrorCode = "TRUST_UPGRADE_FORBIDDEN"
	CodeSegmentTooLarge  ErrorCode = "TRUST_SEGMENT_TOO_LARGE"
	CodeRenderFailed     ErrorCode = "TRUST_RENDER_FAILED"
)

const EventInjectionSuspected = "trustcontent.injection.suspected"

type Error struct{ Code ErrorCode }

func (e *Error) Error() string { return string(e.Code) }
func (e *Error) Is(target error) bool {
	other, ok := target.(*Error)
	return ok && e != nil && other != nil && e.Code == other.Code
}

var (
	ErrZoneInvalid      = &Error{Code: CodeZoneInvalid}
	ErrUpgradeForbidden = &Error{Code: CodeUpgradeForbidden}
	ErrSegmentTooLarge  = &Error{Code: CodeSegmentTooLarge}
	ErrRenderFailed     = &Error{Code: CodeRenderFailed}
)

// Segment is an immutable-in-meaning context item. Zone is assigned solely by
// source transport and never inferred from Content.
type Segment struct {
	Zone     Zone   `json:"zone"`
	SourceID string `json:"source_id"`
	Content  string `json:"content"`
	Digest   string `json:"digest"`
}

// Record is the bounded durable projection. It deliberately has no Content
// field: raw source material is rendered only in the caller's process.
type Record struct {
	ID             string    `json:"id"`
	IdempotencyKey string    `json:"idempotency_key"`
	SourceID       string    `json:"source_id"`
	Zone           Zone      `json:"zone"`
	Digest         string    `json:"digest"`
	ContentRef     string    `json:"content_ref"`
	State          State     `json:"state"`
	CreatedAt      time.Time `json:"created_at"`
}

type IngestRequest struct {
	ID             string `json:"id"`
	IdempotencyKey string `json:"idempotency_key"`
	Source         Source `json:"source"`
	SourceID       string `json:"source_id"`
	Content        string `json:"content"`
	SubjectID      string `json:"subject_id,omitempty"`
	TaskID         string `json:"task_id,omitempty"`
	RunID          string `json:"run_id,omitempty"`
}

type RenderRequest struct {
	SubjectID  string    `json:"subject_id,omitempty"`
	TaskID     string    `json:"task_id,omitempty"`
	RunID      string    `json:"run_id,omitempty"`
	SegmentIDs []string  `json:"segment_ids,omitempty"`
	Segments   []Segment `json:"segments"`
}

type Repository interface {
	PutTrustedContentSegment(context.Context, Record) error
	GetTrustedContentSegment(context.Context, string) (Record, error)
	TransitionTrustedContentSegment(context.Context, string, State, State) error
}

type Authorizer interface {
	AuthorizeTrustContent(context.Context, IngestRequest, Zone) error
}

// Detector is optional observability only. Its result never changes a
// transport-assigned zone or grants authority to source content.
type Detector interface {
	SuspectTrustContent(context.Context, Segment) (bool, error)
}

func (s Segment) Validate() error {
	if !s.Zone.Valid() || !safeIdentifier(s.SourceID) {
		return ErrZoneInvalid
	}
	if !utf8.ValidString(s.Content) || len(s.Content) > MaxSegmentBytes {
		return ErrSegmentTooLarge
	}
	if containsTestSecret(s.Content) {
		return ErrRenderFailed
	}
	if s.Digest != "" && s.Digest != Digest(s.Content) {
		return ErrRenderFailed
	}
	return nil
}

func (r Record) Validate() error {
	if !safeIdentifier(r.ID) || !safeIdentifier(r.IdempotencyKey) || !safeIdentifier(r.SourceID) ||
		!r.Zone.Valid() || !validDigest(r.Digest) || !validDigest(r.ContentRef) || r.Digest != r.ContentRef ||
		!r.State.Valid() || r.CreatedAt.IsZero() || r.CreatedAt.Location() != time.UTC {
		return ErrZoneInvalid
	}
	return nil
}

func (r IngestRequest) Validate() error {
	if !safeIdentifier(r.ID) || !safeIdentifier(r.IdempotencyKey) || !safeIdentifier(r.SourceID) || !r.Source.Valid() {
		return ErrZoneInvalid
	}
	if !utf8.ValidString(r.Content) || len(r.Content) > MaxSegmentBytes {
		return ErrSegmentTooLarge
	}
	if containsTestSecret(r.Content) {
		return ErrRenderFailed
	}
	for _, value := range []string{r.SubjectID, r.TaskID, r.RunID} {
		if value != "" && !safeIdentifier(value) {
			return ErrZoneInvalid
		}
	}
	return nil
}

func (s Source) Valid() bool {
	switch s {
	case SourceSystem, SourceOwnerPolicy, SourceProjectPolicy, SourceTrustedTool, SourceRepository, SourceWeb, SourceMCP, SourceUntrusted:
		return true
	default:
		return false
	}
}

func (s State) Valid() bool {
	return s == StateIngested || s == StateZoned || s == StateRendered
}

func (z Zone) Valid() bool { return z.rank() >= 0 }

func (z Zone) rank() int {
	switch z {
	case System:
		return 0
	case OwnerPolicy:
		return 1
	case ProjectPolicy:
		return 2
	case TrustedTool:
		return 3
	case RepositoryData:
		return 4
	case WebData:
		return 5
	case UntrustedContent:
		return 6
	default:
		return -1
	}
}

// Digest is the canonical content reference persisted and attached to output.
func Digest(content string) string {
	sum := sha256.Sum256([]byte(content))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func validDigest(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	_, err := hex.DecodeString(value[len("sha256:"):])
	return err == nil
}

func safeIdentifier(value string) bool {
	if value == "" || len(value) > 256 || strings.TrimSpace(value) != value || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func containsTestSecret(value string) bool {
	return strings.Contains(strings.ToLower(value), "marshal_test_secret_")
}

var _ error = ErrZoneInvalid
