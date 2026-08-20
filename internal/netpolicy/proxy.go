package netpolicy

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

var (
	ErrForbiddenDestination = errors.New("egress proxy: forbidden destination")
	ErrPrivateIPBlocked     = errors.New("egress proxy: resolved address belongs to a forbidden private or local IP range")
)

var (
	privateIPv4Blocks = []*net.IPNet{
		parseCIDR("10.0.0.0/8"),
		parseCIDR("172.16.0.0/12"),
		parseCIDR("192.168.0.0/16"),
		parseCIDR("100.64.0.0/10"),
		parseCIDR("127.0.0.0/8"),
		parseCIDR("169.254.0.0/16"), // Link-local and AWS/GCP/Azure metadata 169.254.169.254
		parseCIDR("0.0.0.0/8"),
		parseCIDR("224.0.0.0/4"), // Multicast
	}

	privateIPv6Blocks = []*net.IPNet{
		parseCIDR("::1/128"),
		parseCIDR("fe80::/10"), // Link-local
		parseCIDR("fc00::/7"),  // Unique local address
		parseCIDR("ff00::/8"),  // Multicast
		parseCIDR("::/128"),    // Unspecified
	}
)

func parseCIDR(s string) *net.IPNet {
	_, block, err := net.ParseCIDR(s)
	if err != nil {
		panic(fmt.Sprintf("invalid CIDR in netpolicy static tables: %s: %v", s, err))
	}
	return block
}

// IsForbiddenPrivateIP reports whether an IP address belongs to local, loopback,
// link-local, cloud metadata, or private RFC1918/RFC4193 address blocks.
func IsForbiddenPrivateIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip4 := ip.To4(); ip4 != nil {
		for _, block := range privateIPv4Blocks {
			if block.Contains(ip4) {
				return true
			}
		}
		return false
	}
	for _, block := range privateIPv6Blocks {
		if block.Contains(ip) {
			return true
		}
	}
	return false
}

type DecisionStore interface {
	PutEgressDecision(ctx context.Context, record DecisionRecord) error
}

type ProxyConfig struct {
	Evaluator Evaluator
	Store     DecisionStore
	SubjectID string
	TaskID    string
	ChangeID  string
	Resolver  *net.Resolver
	Dialer    *net.Dialer
	Listener  net.Listener
}

type EgressProxy struct {
	evaluator Evaluator
	store     DecisionStore
	subjectID string
	taskID    string
	changeID  string
	resolver  *net.Resolver
	dialer    *net.Dialer
	listener  net.Listener
	server    *http.Server
	addr      string
	mu        sync.Mutex
}

func NewEgressProxy(cfg ProxyConfig) (*EgressProxy, error) {
	if cfg.Evaluator == nil {
		return nil, fmt.Errorf("evaluator is required for EgressProxy")
	}
	resolver := cfg.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	dialer := cfg.Dialer
	if dialer == nil {
		dialer = &net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}
	}

	ln := cfg.Listener
	if ln == nil {
		var err error
		ln, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, fmt.Errorf("listen egress proxy: %w", err)
		}
	}

	p := &EgressProxy{
		evaluator: cfg.Evaluator,
		store:     cfg.Store,
		subjectID: cfg.SubjectID,
		taskID:    cfg.TaskID,
		changeID:  cfg.ChangeID,
		resolver:  resolver,
		dialer:    dialer,
		listener:  ln,
		addr:      ln.Addr().String(),
	}

	p.server = &http.Server{
		Handler:      p,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	return p, nil
}

func (p *EgressProxy) Start() {
	go func() {
		_ = p.server.Serve(p.listener)
	}()
}

func (p *EgressProxy) Addr() string {
	return p.addr
}

func (p *EgressProxy) URL() string {
	return "http://" + p.addr
}

func (p *EgressProxy) Close() error {
	return p.server.Close()
}

func (p *EgressProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		p.handleConnect(w, r)
		return
	}
	p.handleHTTP(w, r)
}

func (p *EgressProxy) handleConnect(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	host, portStr, err := net.SplitHostPort(r.Host)
	if err != nil {
		http.Error(w, "invalid CONNECT host", http.StatusBadRequest)
		return
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		http.Error(w, "invalid CONNECT port", http.StatusBadRequest)
		return
	}

	validatedIP, decision, err := p.evaluateAndResolve(ctx, host, port, ProtocolTCP)
	p.recordDecision(ctx, host, port, validatedIP, decision)

	if err != nil || !decision.Allowed {
		http.Error(w, "egress denied by policy: "+string(decision.Reason), http.StatusForbidden)
		return
	}

	// Dial directly to the validated IP address to prevent TOCTOU DNS rebinding
	targetConn, err := p.dialer.DialContext(ctx, "tcp", net.JoinHostPort(validatedIP.String(), portStr))
	if err != nil {
		http.Error(w, "connection failure: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer targetConn.Close()

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking unsupported", http.StatusInternalServerError)
		return
	}

	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, "hijacking failed", http.StatusInternalServerError)
		return
	}
	defer clientConn.Close()

	// Notify client that CONNECT tunnel is established
	_, _ = clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

	// Bidirectional splice
	errc := make(chan error, 2)
	go func() {
		_, err := io.Copy(targetConn, clientConn)
		errc <- err
	}()
	go func() {
		_, err := io.Copy(clientConn, targetConn)
		errc <- err
	}()
	<-errc
}

func (p *EgressProxy) handleHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	host := r.URL.Hostname()
	if host == "" {
		host = r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
	}
	portStr := r.URL.Port()
	if portStr == "" {
		portStr = "80"
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		http.Error(w, "invalid destination port", http.StatusBadRequest)
		return
	}

	validatedIP, decision, err := p.evaluateAndResolve(ctx, host, port, ProtocolTCP)
	p.recordDecision(ctx, host, port, validatedIP, decision)

	if err != nil || !decision.Allowed {
		http.Error(w, "egress denied by policy: "+string(decision.Reason), http.StatusForbidden)
		return
	}

	// Dial directly to the validated IP address
	targetConn, err := p.dialer.DialContext(ctx, "tcp", net.JoinHostPort(validatedIP.String(), portStr))
	if err != nil {
		http.Error(w, "connection failure: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer targetConn.Close()

	outReq := r.Clone(ctx)
	outReq.RequestURI = ""
	outReq.URL.Scheme = "http"
	outReq.URL.Host = net.JoinHostPort(host, portStr)

	// Remove hop-by-hop headers
	outReq.Header.Del("Proxy-Connection")
	outReq.Header.Del("Proxy-Authenticate")
	outReq.Header.Del("Proxy-Authorization")

	if err := outReq.Write(targetConn); err != nil {
		http.Error(w, "failed to forward HTTP request: "+err.Error(), http.StatusBadGateway)
		return
	}

	resp, err := http.ReadResponse(bufio.NewReader(targetConn), outReq)
	if err != nil {
		http.Error(w, "failed to read HTTP response: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (p *EgressProxy) evaluateAndResolve(ctx context.Context, host string, port int, proto Protocol) (net.IP, Decision, error) {
	// 1. Check if host is an explicit IP literal
	if ip, isIP := parseIPLiteral(host); isIP {
		req := Request{
			SubjectID: p.subjectID,
			TaskID:    p.taskID,
			ChangeID:  p.changeID,
			Host:      ip.String(),
			IP:        ip.String(),
			Protocol:  proto,
			Port:      port,
		}
		decision, err := p.evaluator.Evaluate(ctx, req)
		return ip, decision, err
	}

	// 2. Evaluate hostname policy first
	reqHost := Request{
		SubjectID: p.subjectID,
		TaskID:    p.taskID,
		ChangeID:  p.changeID,
		Host:      host,
		Protocol:  proto,
		Port:      port,
	}
	hostDecision, err := p.evaluator.Evaluate(ctx, reqHost)
	if err != nil || !hostDecision.Allowed {
		return nil, hostDecision, err
	}

	// 3. Controlled DNS resolution
	ips, err := p.resolver.LookupIPAddr(ctx, host)
	if err != nil || len(ips) == 0 {
		return nil, Decision{
			Allowed: false,
			Reason:  ReasonDenied,
			Host:    host,
			Port:    port,
		}, fmt.Errorf("DNS lookup failed for %s: %w", host, err)
	}

	// 4. Validate all resolved IPs to prevent DNS rebinding / SSRF
	var chosenIP net.IP
	for _, resolved := range ips {
		ip := resolved.IP
		if IsForbiddenPrivateIP(ip) {
			// A domain name rule cannot resolve to private/local IPs
			return nil, Decision{
				Allowed: false,
				Reason:  ReasonDenied,
				Host:    host,
				IP:      ip.String(),
				Port:    port,
			}, ErrPrivateIPBlocked
		}
		if chosenIP == nil {
			chosenIP = ip
		}
	}

	return chosenIP, hostDecision, nil
}

func (p *EgressProxy) recordDecision(ctx context.Context, host string, port int, ip net.IP, decision Decision) {
	if p.store == nil {
		return
	}
	var ipStr string
	if ip != nil {
		ipStr = ip.String()
	}
	idBytes := make([]byte, 8)
	_, _ = rand.Read(idBytes)
	recordID := fmt.Sprintf("dec-%x", hex.EncodeToString(idBytes))
	idempotencyKey := fmt.Sprintf("idem-%x", hex.EncodeToString(idBytes))

	record := DecisionRecord{
		ID:             recordID,
		IdempotencyKey: idempotencyKey,
		Request: Request{
			SubjectID: p.subjectID,
			TaskID:    p.taskID,
			ChangeID:  p.changeID,
			Host:      host,
			IP:        ipStr,
			Protocol:  ProtocolTCP,
			Port:      port,
		},
		Decision: Decision{
			Allowed: decision.Allowed,
			RuleID:  decision.RuleID,
			Reason:  decision.Reason,
			Host:    host,
			IP:      ipStr,
			Port:    port,
		},
		CreatedAt: time.Now().UTC(),
	}

	_ = p.store.PutEgressDecision(ctx, record)
}
