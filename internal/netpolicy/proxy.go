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
	"sync/atomic"
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
	Broker            *CredentialBroker
	CredentialAllowed func(context.Context) bool
	Evaluator         Evaluator
	Store             DecisionStore
	SubjectID         string
	TaskID            string
	ChangeID          string
	Resolver          *net.Resolver
	Dialer            *net.Dialer
	Listener          net.Listener
	RunID             string
	Attempt           func(context.Context, string, int, Decision) error
}

type EgressProxy struct {
	dialContext       func(context.Context, string, string) (net.Conn, error)
	broker            *CredentialBroker
	credentialAllowed func(context.Context) bool
	evaluator         Evaluator
	store             DecisionStore
	subjectID         string
	taskID            string
	changeID          string
	resolver          *net.Resolver
	dialer            *net.Dialer
	listener          net.Listener
	server            *http.Server
	addr              string
	mu                sync.Mutex
	runID             string
	attempt           func(context.Context, string, int, Decision) error
	connections       map[net.Conn]string
	closed            bool
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
		dialContext:       dialer.DialContext,
		broker:            cfg.Broker,
		credentialAllowed: cfg.CredentialAllowed,
		evaluator:         cfg.Evaluator,
		store:             cfg.Store,
		subjectID:         cfg.SubjectID,
		taskID:            cfg.TaskID,
		changeID:          cfg.ChangeID,
		runID:             cfg.RunID,
		attempt:           cfg.Attempt,
		connections:       map[net.Conn]string{},
		resolver:          resolver,
		dialer:            dialer,
		listener:          ln,
		addr:              ln.Addr().String(),
	}

	p.listener = &observedListener{Listener: ln, proxy: p}
	p.server = &http.Server{
		ConnContext: func(ctx context.Context, conn net.Conn) context.Context {
			return context.WithValue(ctx, ingressConnectionKey{}, conn)
		},
		Handler:      p,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	p.server.SetKeepAlivesEnabled(false)
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
	p.mu.Lock()
	p.closed = true
	for conn := range p.connections {
		_ = conn.Close()
	}
	p.mu.Unlock()
	return p.server.Close()
}

func (p *EgressProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if c, ok := r.Context().Value(ingressConnectionKey{}).(*observedConnection); ok {
		c.active.Store(true)
	}
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
		p.recordInvalid(ctx, r.Host, 0)
		http.Error(w, "invalid CONNECT host", http.StatusBadRequest)
		return
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		p.recordInvalid(ctx, host, port)
		http.Error(w, "invalid CONNECT port", http.StatusBadRequest)
		return
	}

	// Refuse the known Claude token host before dialing, even if egress is
	// granted. TLS termination remains confined to the Anthropic API host.
	if p.broker.refusesAuthHost(host) {
		if p.recordDecision(ctx, host, port, nil, Decision{Reason: ReasonBrokerRefreshDenied}) != nil {
			http.Error(w, "credential broker evidence unavailable", http.StatusServiceUnavailable)
		} else {
			http.Error(w, "credential broker sandbox refresh refused", http.StatusForbidden)
		}
		return
	}
	validatedIP, decision, err := p.evaluateAndResolve(ctx, host, port, ProtocolTCP)
	if recordErr := p.recordDecision(ctx, host, port, validatedIP, decision); recordErr != nil {
		http.Error(w, "egress evidence unavailable", http.StatusServiceUnavailable)
		return
	}

	if err != nil || !decision.Allowed {
		http.Error(w, "egress denied by policy: "+string(decision.Reason), http.StatusForbidden)
		return
	}

	// Dial directly to the validated IP address to prevent TOCTOU DNS rebinding
	targetConn, err := p.connect(ctx, host, port, validatedIP)
	if err != nil {
		http.Error(w, "connection failure: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer p.disconnect(targetConn)

	if p.broker.handles(host) {
		if port != 443 {
			http.Error(w, "credential broker requires provider port 443", http.StatusForbidden)
			return
		}
		p.handleBrokerConnect(w, r, host, port, targetConn)
		return
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking unsupported", http.StatusInternalServerError)
		return
	}

	clientConn, buffered, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, "hijacking failed", http.StatusInternalServerError)
		return
	}
	defer clientConn.Close()
	// Hijacked CONNECT streams outlive the HTTP header deadline.
	_ = clientConn.SetDeadline(time.Time{})

	// Notify client that CONNECT tunnel is established
	_, _ = clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

	// Bidirectional splice
	errc := make(chan error, 2)
	go func() {
		_, err := io.Copy(targetConn, buffered)
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
	if r.URL.Scheme != "http" || r.URL.Host == "" || r.URL.User != nil || r.Host != r.URL.Host {
		p.recordInvalid(ctx, r.Host, 0)
		http.Error(w, "absolute HTTP proxy URL required", http.StatusBadRequest)
		return
	}
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
		p.recordInvalid(ctx, host, port)
		http.Error(w, "invalid destination port", http.StatusBadRequest)
		return
	}

	// Refuse the known Claude token host before dialing, even if egress is
	// granted. TLS termination remains confined to the Anthropic API host.
	if p.broker.refusesAuthHost(host) {
		if p.recordDecision(ctx, host, port, nil, Decision{Reason: ReasonBrokerRefreshDenied}) != nil {
			http.Error(w, "credential broker evidence unavailable", http.StatusServiceUnavailable)
		} else {
			http.Error(w, "credential broker sandbox refresh refused", http.StatusForbidden)
		}
		return
	}
	validatedIP, decision, err := p.evaluateAndResolve(ctx, host, port, ProtocolTCP)
	if recordErr := p.recordDecision(ctx, host, port, validatedIP, decision); recordErr != nil {
		http.Error(w, "egress evidence unavailable", http.StatusServiceUnavailable)
		return
	}

	if err != nil || !decision.Allowed {
		http.Error(w, "egress denied by policy: "+string(decision.Reason), http.StatusForbidden)
		return
	}

	if p.broker.handles(host) {
		http.Error(w, "credential broker requires HTTPS", http.StatusForbidden)
		return
	}

	// Dial directly to the validated IP address
	targetConn, err := p.connect(ctx, host, port, validatedIP)
	if err != nil {
		http.Error(w, "connection failure: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer p.disconnect(targetConn)

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
			RunID:     p.runID,
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
		RunID:     p.runID,
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

func (p *EgressProxy) recordDecision(ctx context.Context, host string, port int, ip net.IP, decision Decision) error {
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
			RunID:     p.runID,
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

	if p.store != nil && record.Validate() == nil {
		if err := p.store.PutEgressDecision(ctx, record); err != nil {
			return err
		}
	}
	if p.attempt != nil {
		return p.attempt(ctx, host, port, decision)
	}
	return nil
}

// CloseEndpoint ends live tunnels and forwards after an operator revocation.
func (p *EgressProxy) CloseEndpoint(endpoint string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for conn, destination := range p.connections {
		if destination == endpoint {
			_ = conn.Close()
		}
	}
}

func (p *EgressProxy) connect(ctx context.Context, host string, port int, ip net.IP) (net.Conn, error) {
	// Registration and revocation share this lock. Recheck authority after DNS
	// and evidence persistence so a concurrent revoke cannot leave a live tunnel.
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, ErrEnforcementUnavailable
	}
	d, err := p.evaluator.Evaluate(ctx, Request{SubjectID: p.subjectID, TaskID: p.taskID, Host: host, Port: port, Protocol: ProtocolTCP})
	if err != nil || !d.Allowed {
		return nil, ErrDenied
	}
	conn, err := p.dialContext(ctx, "tcp", net.JoinHostPort(ip.String(), strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}
	endpoint, err := Endpoint(net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	p.connections[conn] = endpoint
	return conn, nil
}

func (p *EgressProxy) disconnect(conn net.Conn) {
	_ = conn.Close()
	p.mu.Lock()
	delete(p.connections, conn)
	p.mu.Unlock()
}

func (p *EgressProxy) recordInvalid(ctx context.Context, host string, port int) {
	if p.attempt != nil {
		_ = p.attempt(ctx, host, port, Decision{Host: host, Port: port, Reason: ReasonRuleInvalid})
	}
}

// Each ingress connection carries one request. Parser failures occur before
// ServeHTTP, so the connection records bytes and whether a handler ran.
// Empty bridge readiness probes are not requests.
type ingressConnectionKey struct{}
type observedListener struct {
	net.Listener
	proxy *EgressProxy
}

func (l *observedListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &observedConnection{Conn: c, proxy: l.proxy}, nil
}

type observedConnection struct {
	net.Conn
	proxy  *EgressProxy
	bytes  atomic.Int64
	active atomic.Bool
	once   sync.Once
}

func (c *observedConnection) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	c.bytes.Add(int64(n))
	return n, err
}
func (c *observedConnection) Close() error {
	c.once.Do(func() {
		if c.bytes.Load() > 0 && !c.active.Load() {
			c.proxy.recordInvalid(context.Background(), "invalid-request", 0)
		}
	})
	return c.Conn.Close()
}
