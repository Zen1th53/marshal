package netpolicy

import (
	"context"
	"net"
	"strings"
	"time"
)

type RuleID string
type Protocol string
type Action string
type Reason string

const (
	ProtocolTCP Protocol = "tcp"
	ProtocolUDP Protocol = "udp"

	ActionAllow Action = "allow"
	ActionDeny  Action = "deny"

	ReasonAllowed                Reason = "NET_ALLOWED"
	ReasonDenied                 Reason = "NET_DENIED"
	ReasonRuleInvalid            Reason = "NET_RULE_INVALID"
	ReasonProtocolDenied         Reason = "NET_PROTOCOL_DENIED"
	ReasonRedirectDenied         Reason = "NET_REDIRECT_DENIED"
	ReasonEnforcementUnavailable Reason = "NET_ENFORCEMENT_UNAVAILABLE"
)

type ErrorCode string

const (
	CodeDenied                 ErrorCode = "NET_DENIED"
	CodeRuleInvalid            ErrorCode = "NET_RULE_INVALID"
	CodeProtocolDenied         ErrorCode = "NET_PROTOCOL_DENIED"
	CodeRedirectDenied         ErrorCode = "NET_REDIRECT_DENIED"
	CodeEnforcementUnavailable ErrorCode = "NET_ENFORCEMENT_UNAVAILABLE"
)

type Error struct {
	Code ErrorCode
}

func (e *Error) Error() string { return string(e.Code) }
func (e *Error) Is(target error) bool {
	other, ok := target.(*Error)
	return ok && e != nil && other != nil && e.Code == other.Code
}

var (
	ErrDenied                 = &Error{Code: CodeDenied}
	ErrRuleInvalid            = &Error{Code: CodeRuleInvalid}
	ErrProtocolDenied         = &Error{Code: CodeProtocolDenied}
	ErrRedirectDenied         = &Error{Code: CodeRedirectDenied}
	ErrEnforcementUnavailable = &Error{Code: CodeEnforcementUnavailable}
)

type Rule struct {
	ID          RuleID   `json:"id"`
	HostPattern string   `json:"host_pattern"`
	Protocol    Protocol `json:"protocol"`
	Ports       []int    `json:"ports"`
	Action      Action   `json:"action"`
}

func (r Rule) Validate() error {
	if !validIdentifier(string(r.ID)) || !validHostPattern(r.HostPattern) || len(r.Ports) == 0 {
		return ErrRuleInvalid
	}
	if !validProtocol(r.Protocol) {
		return ErrProtocolDenied
	}
	if r.Action != ActionAllow && r.Action != ActionDeny {
		return ErrRuleInvalid
	}
	seen := make(map[int]struct{}, len(r.Ports))
	for _, port := range r.Ports {
		if port < 1 || port > 65535 {
			return ErrRuleInvalid
		}
		if _, exists := seen[port]; exists {
			return ErrRuleInvalid
		}
		seen[port] = struct{}{}
	}
	return nil
}

type Request struct {
	Host     string   `json:"host"`
	IP       string   `json:"ip,omitempty"`
	Protocol Protocol `json:"protocol"`
	Port     int      `json:"port"`
}

func (r Request) Validate() error {
	if !validHost(r.Host) || r.Port < 1 || r.Port > 65535 {
		return ErrRuleInvalid
	}
	if r.IP != "" && net.ParseIP(r.IP) == nil {
		return ErrRuleInvalid
	}
	if !validProtocol(r.Protocol) {
		return ErrProtocolDenied
	}
	return nil
}

type Decision struct {
	Allowed bool   `json:"allowed"`
	RuleID  RuleID `json:"rule_id,omitempty"`
	Reason  Reason `json:"reason"`
	Host    string `json:"host"`
	IP      string `json:"ip,omitempty"`
	Port    int    `json:"port"`
}

// DecisionRecord is a bounded durable projection of one normalized request
// and its decision. Rule definitions remain owned by the active policy
// configuration; this record is for restart/retry reconstruction only.
type DecisionRecord struct {
	ID             string    `json:"id"`
	IdempotencyKey string    `json:"idempotency_key"`
	Request        Request   `json:"request"`
	Decision       Decision  `json:"decision"`
	CreatedAt      time.Time `json:"created_at"`
}

func (r DecisionRecord) Validate() error {
	if !validIdentifier(r.ID) || !validIdentifier(r.IdempotencyKey) || r.CreatedAt.IsZero() || r.CreatedAt.Location() != time.UTC {
		return ErrRuleInvalid
	}
	if err := r.Request.Validate(); err != nil {
		return err
	}
	if err := r.Decision.Validate(); err != nil {
		return err
	}
	if r.Decision.Host != r.Request.Host || r.Decision.IP != r.Request.IP || r.Decision.Port != r.Request.Port {
		return ErrRuleInvalid
	}
	return nil
}

func (d Decision) Validate() error {
	if !validHost(d.Host) || d.Port < 1 || d.Port > 65535 || d.IP != "" && net.ParseIP(d.IP) == nil {
		return ErrRuleInvalid
	}
	if d.Allowed {
		if !validIdentifier(string(d.RuleID)) || d.Reason != ReasonAllowed {
			return ErrRuleInvalid
		}
		return nil
	}
	switch d.Reason {
	case ReasonDenied, ReasonRuleInvalid, ReasonProtocolDenied, ReasonRedirectDenied, ReasonEnforcementUnavailable:
		return nil
	default:
		return ErrRuleInvalid
	}
}

type Evaluator interface {
	Evaluate(context.Context, Request) (Decision, error)
}

func validProtocol(protocol Protocol) bool {
	return protocol == ProtocolTCP || protocol == ProtocolUDP
}

func validHostPattern(value string) bool {
	value = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".")
	if value == "" || strings.ContainsAny(value, "/:@ \t\r\n") {
		return false
	}
	if strings.HasPrefix(value, "*.") {
		value = strings.TrimPrefix(value, "*.")
	}
	if strings.Contains(value, "*") || net.ParseIP(value) != nil {
		return net.ParseIP(value) != nil
	}
	labels := strings.Split(value, ".")
	for _, label := range labels {
		if label == "" || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return false
			}
		}
	}
	return true
}

func validHost(value string) bool {
	value = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".")
	if net.ParseIP(value) != nil {
		return true
	}
	return validHostPattern(value)
}

func validIdentifier(value string) bool {
	return value != "" && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "/\\\t\r\n")
}

var _ error = ErrDenied
var _ Evaluator
