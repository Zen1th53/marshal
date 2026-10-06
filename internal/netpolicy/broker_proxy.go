package netpolicy

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/redaction"
)

const brokerBodyLimit = 32 << 20

type bufferedBrokerConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedBrokerConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

// Only an already authorized, resolved and pinned CONNECT can reach this path.
// Both sides verify TLS; the worker's inner authority is bound to CONNECT.
// A single request per connection avoids changing authority under a live grant.
func (p *EgressProxy) handleBrokerConnect(w http.ResponseWriter, r *http.Request, host string, port int, target net.Conn) {
	b := p.broker
	if p.credentialAllowed != nil && !p.credentialAllowed(r.Context()) {
		http.Error(w, "credential broker permission revoked", 403)
		return
	}
	cert, err := b.certificate(host)
	if err != nil {
		http.Error(w, "credential broker TLS unavailable", 503)
		return
	}
	upstream := tls.Client(target, &tls.Config{ServerName: host, RootCAs: b.upstreamRoots, MinVersion: tls.VersionTLS12})
	_ = upstream.SetDeadline(time.Now().Add(30 * time.Second))
	if err = upstream.HandshakeContext(r.Context()); err != nil {
		http.Error(w, "credential broker upstream TLS refused", 502)
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "credential broker transport unavailable", 503)
		return
	}
	client, buffer, err := hijacker.Hijack()
	if err != nil {
		return
	}
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(30 * time.Second))
	if _, err = client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		return
	}
	secured := tls.Server(&bufferedBrokerConn{Conn: client, reader: buffer.Reader}, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12, GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
		if hello.ServerName != "" && hello.ServerName != host {
			return nil, ErrCredentialBroker
		}
		return nil, nil
	}})
	if err = secured.HandshakeContext(r.Context()); err != nil {
		return
	}
	req, err := http.ReadRequest(bufio.NewReader(secured))
	if err != nil {
		return
	}
	defer req.Body.Close()
	authority := net.JoinHostPort(host, strconv.Itoa(port))
	validHost := req.Host == authority || (port == 443 && req.Host == host)
	if !validHost || req.URL.IsAbs() || req.Method == http.MethodConnect || req.Header.Get("Upgrade") != "" {
		brokerError(secured, 403, "credential broker authority or protocol refused")
		return
	}
	// Revocation is checked again immediately before substitution. connect() also
	// registers the upstream socket so revoke interrupts in-flight exchanges.
	d, err := p.evaluator.Evaluate(context.Background(), Request{SubjectID: p.subjectID, TaskID: p.taskID, Host: host, Port: port, Protocol: ProtocolTCP})
	if err != nil || !d.Allowed || (p.credentialAllowed != nil && !p.credentialAllowed(r.Context())) {
		brokerError(secured, 403, "credential broker permission revoked")
		return
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, brokerBodyLimit+1))
	if err != nil || len(body) > brokerBodyLimit {
		brokerError(secured, 413, "credential broker request too large")
		return
	}
	if rule := brokerRefreshRule(req, body); b.subscription != nil && rule != "" {
		if err := p.recordDecision(r.Context(), host, port, nil, Decision{Reason: ReasonBrokerRefreshDenied, RuleID: RuleID(rule)}); err != nil {
			brokerError(secured, 503, "credential broker evidence unavailable")
		} else {
			brokerError(secured, 403, "credential broker sandbox refresh refused")
		}
		return
	}
	// The Codex auth host is intercepted solely to refuse worker token calls.
	// Keep every other route there closed too, without classifying it as refresh
	// or ever injecting the host access token into an authentication service.
	if b.subscription != nil && host == "auth.openai.com" {
		if err := p.recordDecision(r.Context(), host, port, nil, Decision{Reason: ReasonDenied, RuleID: "credential-injection-host"}); err != nil {
			brokerError(secured, 503, "credential broker evidence unavailable")
		} else {
			brokerError(secured, 403, "credential broker authentication host refused")
		}
		return
	}
	if strings.Contains(string(body), b.placeholder) {
		brokerError(secured, 403, "credential broker unsupported credential body field")
		return
	}
	_ = secured.SetDeadline(time.Now().Add(30 * time.Minute))
	_ = upstream.SetDeadline(time.Now().Add(30 * time.Minute))
	stripBrokerHopHeaders(req.Header)
	req.Header.Set("Accept-Encoding", "identity")
	req.RequestURI = ""
	req.Close = true
	req.ContentLength = int64(len(body))
	req.TransferEncoding = nil
	secrets := []string{b.secret}
	replacements := map[string]string{b.secret: b.placeholder}
	capture := func(c subscriptionCredential) {
		placeholders := []string{b.placeholder, b.refreshPlaceholder, b.idPlaceholder, b.scratchName}
		for i, secret := range c.secrets {
			replacements[secret] = placeholders[i]
		}
		secrets = append(secrets, c.secrets...)
	}
	var resp *http.Response
	var generation uint64
	if b.subscription != nil {
		generation = b.subscription.generation()
	}
	refreshed := false
	credential := subscriptionCredential{access: b.secret}
	if b.subscription != nil {
		if req.Header.Get("Authorization") != "Bearer "+b.placeholder {
			brokerError(secured, 403, "credential broker subscription placeholder required")
			return
		}
		credential, err = b.subscription.read()
		capture(credential)
		if err == nil && time.Until(credential.expires) <= 2*time.Minute {
			if p.credentialAllowed != nil && !p.credentialAllowed(r.Context()) {
				brokerError(secured, 403, "credential broker permission revoked")
				return
			}
			credential, err = b.subscription.renew(r.Context(), generation, credential.access)
			refreshed = true
		}
		if err != nil || time.Until(credential.expires) <= 2*time.Minute {
			p.alertClaude(r.Context(), host, port)
			brokerError(secured, 503, "credential broker host sign-in refresh unavailable")
			return
		}
		capture(credential)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if p.credentialAllowed != nil && !p.credentialAllowed(r.Context()) {
			brokerError(secured, 403, "credential broker permission revoked")
			return
		}
		if b.subscription != nil {
			req.Header.Set("Authorization", "Bearer "+credential.access)
			if !b.subscription.claude && (req.Header.Get("Chatgpt-Account-Id") == b.scratchName || attempt == 1) {
				req.Header.Set("Chatgpt-Account-Id", credential.account)
			}
		} else {
			expected, replacement := b.placeholder, credential.access
			if b.header == "Authorization" {
				expected, replacement = "Bearer "+expected, "Bearer "+replacement
			}
			if req.Header.Get(b.header) == expected {
				req.Header.Set(b.header, replacement)
			}
			if b.header == "X-Goog-Api-Key" {
				query := req.URL.Query()
				if query.Get("key") == b.placeholder {
					query.Set("key", credential.access)
					req.URL.RawQuery = query.Encode()
				}
			}
		}
		req.Body = io.NopCloser(bytes.NewReader(body))
		if err = req.Write(upstream); err != nil {
			brokerError(secured, 502, "credential broker upstream request failed")
			return
		}
		resp, err = http.ReadResponse(bufio.NewReader(upstream), req)
		if err != nil {
			brokerError(secured, 502, "credential broker upstream response failed")
			return
		}
		if resp.StatusCode == 401 && b.subscription != nil && b.subscription.claude && (refreshed || attempt == 1) {
			p.alertClaude(r.Context(), host, port)
			brokerError(secured, 503, "credential broker host sign-in refresh unavailable")
			_ = resp.Body.Close()
			return
		}
		if resp.StatusCode != 401 || b.subscription == nil || refreshed || attempt == 1 {
			break
		}
		// Discard the rejected exchange without draining an untrusted body or
		// waiting for a TLS close-notify from a peer that is also closing.
		_ = upstream.NetConn().Close()
		_ = resp.Body.Close()
		if p.credentialAllowed != nil && !p.credentialAllowed(r.Context()) {
			brokerError(secured, 403, "credential broker permission revoked")
			return
		}
		credential, err = b.subscription.renew(r.Context(), generation, credential.access)
		if err != nil || time.Until(credential.expires) <= 2*time.Minute {
			p.alertClaude(r.Context(), host, port)
			brokerError(secured, 503, "credential broker host sign-in refresh unavailable")
			return
		}
		refreshed = true
		capture(credential)
		// Re-authorize and pin a new connection for the one retry. It participates
		// in normal revocation and shutdown, just like the first connection.
		ip, decision, resolveErr := p.evaluateAndResolve(r.Context(), host, port, ProtocolTCP)
		if resolveErr != nil || !decision.Allowed || p.recordDecision(r.Context(), host, port, ip, decision) != nil {
			brokerError(secured, 403, "credential broker retry refused")
			return
		}
		target, err = p.connect(r.Context(), host, port, ip)
		if err != nil {
			brokerError(secured, 502, "credential broker retry unavailable")
			return
		}
		defer p.disconnect(target)
		upstream = tls.Client(target, &tls.Config{ServerName: host, RootCAs: b.upstreamRoots, MinVersion: tls.VersionTLS12})
		_ = upstream.SetDeadline(time.Now().Add(30 * time.Second))
		if upstream.HandshakeContext(r.Context()) != nil {
			brokerError(secured, 502, "credential broker retry TLS refused")
			return
		}
		_ = upstream.SetDeadline(time.Now().Add(30 * time.Minute))
	}
	defer resp.Body.Close()
	// Buffer before exposing any bytes. Unknown encodings or oversized bodies
	// fail closed rather than passing an uninspected token-bearing response.
	if encoding := resp.Header.Get("Content-Encoding"); encoding != "" && encoding != "identity" {
		brokerError(secured, 502, "credential broker unsupported response encoding")
		return
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, brokerBodyLimit+1))
	if err != nil || len(data) > brokerBodyLimit {
		brokerError(secured, 502, "credential broker response unavailable or too large")
		return
	}
	clean := b.sanitizeMappedSecrets(string(data), replacements)
	for key, values := range resp.Header {
		if containsBrokerSecret(key, secrets) || redaction.DetectCredentialShape(key) != "" {
			delete(resp.Header, key)
			continue
		}
		for i, value := range values {
			values[i] = b.sanitizeMappedSecrets(value, replacements)
		}
		resp.Header[key] = values
	}
	resp.Status = b.sanitizeMappedSecrets(resp.Status, replacements)
	stripBrokerHopHeaders(resp.Header)
	resp.Trailer = nil
	resp.TransferEncoding = nil
	resp.Close = true
	resp.Body = io.NopCloser(strings.NewReader(clean))
	resp.ContentLength = int64(len(clean))
	resp.Header.Set("Content-Length", strconv.Itoa(len(clean)))
	_ = resp.Write(secured)
}

func (b *CredentialBroker) sanitize(value string) string {
	return b.sanitizeSecrets(value, []string{b.secret})
}

func containsBrokerSecret(value string, secrets []string) bool {
	for _, secret := range secrets {
		if secret != "" && strings.Contains(value, secret) {
			return true
		}
	}
	return false
}

// Paths verified in the installed Codex 0.160.1 and Claude Code 2.1.290
// binaries. OAuth API routes such as roles are not token endpoints.
func brokerRefreshRule(req *http.Request, body []byte) string {
	switch path.Clean(req.URL.Path) {
	case "/oauth/token", "/v1/oauth/token":
		return "oauth-token-path"
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(body, &object) == nil {
		var grant string
		if json.Unmarshal(object["grant_type"], &grant) == nil && grant == "refresh_token" {
			return "refresh-grant-json"
		}
		return ""
	}
	if form, err := url.ParseQuery(string(body)); err == nil {
		for _, grant := range form["grant_type"] {
			if grant == "refresh_token" {
				return "refresh-grant-form"
			}
		}
	}
	return ""
}

func (b *CredentialBroker) sanitizeSecrets(value string, secrets []string) string {
	replacements := make(map[string]string, len(secrets))
	for _, secret := range secrets {
		replacements[secret] = b.placeholder
	}
	return b.sanitizeMappedSecrets(value, replacements)
}

func (b *CredentialBroker) sanitizeMappedSecrets(value string, secrets map[string]string) string {
	// Request-local snapshots scrub both sides of a rotation. Match longest
	// variants first, in one pass, so account substrings and replacements cannot
	// corrupt a different field's placeholder.
	variants := map[string]string{}
	for secret, placeholder := range secrets {
		if secret == "" {
			continue
		}
		encodedJSON, _ := json.Marshal(secret)
		for _, variant := range []string{url.QueryEscape(secret), url.PathEscape(secret), base64.StdEncoding.EncodeToString([]byte(secret)), base64.RawURLEncoding.EncodeToString([]byte(secret)), hex.EncodeToString([]byte(secret)), string(encodedJSON[1 : len(encodedJSON)-1]), secret} {
			variants[variant] = placeholder
		}
	}
	keys := make([]string, 0, len(variants))
	for variant := range variants {
		keys = append(keys, variant)
	}
	sort.Slice(keys, func(i, j int) bool {
		if len(keys[i]) == len(keys[j]) {
			return keys[i] < keys[j]
		}
		return len(keys[i]) > len(keys[j])
	})
	var pairs []string
	for _, key := range keys {
		pairs = append(pairs, key, variants[key])
	}
	return redaction.RedactContent(strings.NewReplacer(pairs...).Replace(value), nil)
}

func brokerError(conn net.Conn, status int, message string) {
	resp := &http.Response{StatusCode: status, ProtoMajor: 1, ProtoMinor: 1, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(message + "\n")), ContentLength: int64(len(message) + 1), Close: true}
	_ = resp.Write(conn)
}
func stripBrokerHopHeaders(header http.Header) {
	for _, name := range strings.Split(header.Get("Connection"), ",") {
		header.Del(strings.TrimSpace(name))
	}
	for _, name := range []string{"Connection", "Proxy-Connection", "Proxy-Authorization", "Proxy-Authenticate", "Keep-Alive", "TE", "Trailer", "Transfer-Encoding", "Upgrade"} {
		header.Del(name)
	}
}
