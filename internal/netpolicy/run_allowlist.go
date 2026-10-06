package netpolicy

import (
	"context"
	"net"
	"strconv"
	"strings"
	"sync"
)

// RunAllowlist permits only exact TCP endpoints. DNS never grants an IP rule
// authority over a name, or a name rule authority over a literal IP.
type RunAllowlist struct {
	mu        sync.RWMutex
	endpoints map[string]bool
}

// Endpoint normalizes an exact host[:port], defaulting an omitted port to 443.
func Endpoint(value string) (string, error) {
	if value == "" || strings.TrimSpace(value) != value || strings.ContainsAny(value, "*/@%\\\t\r\n ") {
		return "", ErrRuleInvalid
	}
	host, port := value, "443"
	if h, p, err := net.SplitHostPort(value); err == nil {
		host, port = h, p
	} else if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
		host = value[1 : len(value)-1]
		if net.ParseIP(host) == nil {
			return "", ErrRuleInvalid
		}
	} else if strings.Contains(value, ":") && net.ParseIP(value) == nil {
		return "", ErrRuleInvalid
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return "", ErrRuleInvalid
	}
	host = normalizeHost(host)
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
	} else {
		if len(host) > 253 || !validHost(host) || strings.ContainsAny(host, "[]:") || strings.Trim(host, "0123456789.") == "" {
			return "", ErrRuleInvalid
		}
		for _, label := range strings.Split(host, ".") {
			if len(label) > 63 {
				return "", ErrRuleInvalid
			}
		}
	}
	return net.JoinHostPort(host, strconv.Itoa(n)), nil
}

func NewRunAllowlist(defaults []string) (*RunAllowlist, error) {
	a := &RunAllowlist{endpoints: map[string]bool{}}
	for _, value := range defaults {
		endpoint, err := Endpoint(value)
		if err != nil {
			return nil, err
		}
		a.endpoints[endpoint] = true
	}
	return a, nil
}

// Set records the operator decision before changing authority. Failed evidence
// persistence cannot open an endpoint. The runtime supplies the authenticated
// operator boundary; this policy object does not consume conversation text.
func (a *RunAllowlist) Set(value string, allow bool, record func(string) error) error {
	endpoint, err := Endpoint(value)
	if err != nil {
		return err
	}
	if record == nil {
		return ErrEnforcementUnavailable
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := record(endpoint); err != nil {
		return err
	}
	if allow {
		a.endpoints[endpoint] = true
	} else {
		delete(a.endpoints, endpoint)
	}
	return nil
}

func (a *RunAllowlist) Endpoints() []string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	var endpoints []string
	for endpoint := range a.endpoints {
		endpoints = append(endpoints, endpoint)
	}
	return endpoints
}

func (a *RunAllowlist) Evaluate(ctx context.Context, r Request) (Decision, error) {
	d := Decision{Host: r.Host, IP: r.IP, Port: r.Port, Reason: ReasonDenied}
	if err := ctx.Err(); err != nil {
		return d, err
	}
	if r.Protocol != ProtocolTCP {
		return d, ErrProtocolDenied
	}
	endpoint, err := Endpoint(net.JoinHostPort(r.Host, strconv.Itoa(r.Port)))
	if err != nil {
		return d, err
	}
	a.mu.RLock()
	d.Allowed = a.endpoints[endpoint]
	a.mu.RUnlock()
	if d.Allowed {
		d.Reason = ReasonAllowed
		d.RuleID = "run-exact-endpoint"
	}
	return d, nil
}
