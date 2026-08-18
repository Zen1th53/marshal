package attestation

type Code string

const (
	CodeNonceReplay          Code = "ATTEST_NONCE_REPLAY"
	CodeStale                Code = "ATTEST_STALE"
	CodePolicyMismatch       Code = "ATTEST_POLICY_MISMATCH"
	CodeCapabilityUnverified Code = "ATTEST_CAPABILITY_UNVERIFIED"
	CodeVersionUnsupported   Code = "ATTEST_VERSION_UNSUPPORTED"
)

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

var (
	ErrNonceReplay          = &Error{Code: CodeNonceReplay, Message: "attestation challenge nonce replay detected"}
	ErrStale                = &Error{Code: CodeStale, Message: "attestation report timestamp is stale"}
	ErrPolicyMismatch       = &Error{Code: CodePolicyMismatch, Message: "attestation policy digest mismatch"}
	ErrCapabilityUnverified = &Error{Code: CodeCapabilityUnverified, Message: "unverified sandbox or runtime capabilities"}
	ErrVersionUnsupported   = &Error{Code: CodeVersionUnsupported, Message: "unsupported MARSHAL version for remote worker"}
)

type Report struct {
	NodeID         string   `json:"node_id"`
	MarshalVersion string   `json:"marshal_version"`
	OS             string   `json:"os"`
	Arch           string   `json:"arch"`
	RuntimeDigest  string   `json:"runtime_digest"`
	PolicyDigest   string   `json:"policy_digest"`
	Nonce          string   `json:"nonce"`
	EvidenceIDs    []string `json:"evidence_ids,omitempty"`
}

type Verdict struct {
	Trusted   bool     `json:"trusted"`
	Reasons   []string `json:"reasons,omitempty"`
	ExpiresAt int64    `json:"expires_at"`
}
