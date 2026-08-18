package router

type Code string

const (
	CodeNoProvider         Code = "ROUTER_NO_PROVIDER"
	CodeProviderDisabled   Code = "ROUTER_PROVIDER_DISABLED"
	CodeCapabilityMismatch Code = "ROUTER_CAPABILITY_MISMATCH"
	CodeContextTooSmall    Code = "ROUTER_CONTEXT_TOO_SMALL"
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
	ErrNoProvider         = &Error{Code: CodeNoProvider, Message: "no suitable provider available for route request"}
	ErrProviderDisabled   = &Error{Code: CodeProviderDisabled, Message: "requested provider is disabled by policy"}
	ErrCapabilityMismatch = &Error{Code: CodeCapabilityMismatch, Message: "model capability mismatch"}
	ErrContextTooSmall    = &Error{Code: CodeContextTooSmall, Message: "model max context is too small for prompt"}
)

type ModelProfile struct {
	Provider     string   `json:"provider"`
	Model        string   `json:"model"`
	Capabilities []string `json:"capabilities,omitempty"`
	MaxContext   int      `json:"max_context"`
	CostClass    string   `json:"cost_class"`
	LatencyClass string   `json:"latency_class"`
}

type RouteDecision struct {
	Provider string   `json:"provider"`
	Model    string   `json:"model"`
	Score    float64  `json:"score"`
	Reasons  []string `json:"reasons,omitempty"`
}
