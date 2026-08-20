package netpolicy_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/netpolicy"
)

type memoryDecisionStore struct {
	mu        sync.Mutex
	decisions []netpolicy.DecisionRecord
}

func (m *memoryDecisionStore) PutEgressDecision(ctx context.Context, record netpolicy.DecisionRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.decisions = append(m.decisions, record)
	return nil
}

func TestEgressProxyHTTPAllowedAndDenied(t *testing.T) {
	// 1. Target HTTP backend
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Backend-Response", "ok")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("backend hello"))
	}))
	defer backend.Close()

	backendURL, _ := url.Parse(backend.URL)
	backendHost, backendPortStr, _ := net.SplitHostPort(backendURL.Host)
	backendPort := 80
	if p, err := net.LookupPort("tcp", backendPortStr); err == nil {
		backendPort = p
	}

	// 2. Policy: Allow backend IP & port, Deny evil.com
	engine, err := netpolicy.NewEvaluator([]netpolicy.Rule{
		{
			ID:          "rule-allow-backend",
			HostPattern: backendHost,
			Protocol:    netpolicy.ProtocolTCP,
			Ports:       []int{backendPort},
			Action:      netpolicy.ActionAllow,
		},
		{
			ID:          "rule-deny-evil",
			HostPattern: "evil.com",
			Protocol:    netpolicy.ProtocolTCP,
			Ports:       []int{80, 443},
			Action:      netpolicy.ActionDeny,
		},
	})
	if err != nil {
		t.Fatalf("NewEvaluator: %v", err)
	}

	store := &memoryDecisionStore{}
	proxy, err := netpolicy.NewEgressProxy(netpolicy.ProxyConfig{
		Evaluator: engine,
		Store:     store,
		TaskID:    "TASK-TEST-01",
		SubjectID: "agent-1",
	})
	if err != nil {
		t.Fatalf("NewEgressProxy: %v", err)
	}
	proxy.Start()
	defer proxy.Close()

	proxyURL, _ := url.Parse(proxy.URL())
	transport := &http.Transport{
		Proxy: http.ProxyURL(proxyURL),
	}
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}

	// 3. Allowed Request through Proxy
	resp, err := client.Get(backend.URL + "/test-path")
	if err != nil {
		t.Fatalf("client.Get allowed backend: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK from backend, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "backend hello" {
		t.Fatalf("unexpected body: %s", body)
	}

	// 4. Denied Request through Proxy
	deniedResp, err := client.Get("http://evil.com/malware")
	if err != nil {
		// Connection or HTTP rejection is expected
	} else {
		defer deniedResp.Body.Close()
		if deniedResp.StatusCode != http.StatusForbidden {
			t.Fatalf("expected 403 Forbidden for evil.com, got %d", deniedResp.StatusCode)
		}
	}

	// 5. Verify Decision Audit Records
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.decisions) == 0 {
		t.Fatal("expected persisted egress decisions in store, got 0")
	}

	foundAllowed := false
	foundDenied := false
	for _, d := range store.decisions {
		if d.Decision.Allowed {
			foundAllowed = true
		} else {
			foundDenied = true
		}
	}
	if !foundAllowed || !foundDenied {
		t.Fatalf("expected both allowed and denied decisions recorded, got: %+v", store.decisions)
	}
}

func TestEgressProxyCONNECTTunnel(t *testing.T) {
	// 1. Target TCP Echo/HTTP server
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				r := bufio.NewReader(c)
				line, _ := r.ReadString('\n')
				if strings.HasPrefix(line, "PING") {
					_, _ = c.Write([]byte("PONG\n"))
				}
			}(conn)
		}
	}()

	host, portStr, _ := net.SplitHostPort(ln.Addr().String())
	var port int
	fmt.Sscanf(portStr, "%d", &port)

	// 2. Policy: Allow target
	engine, err := netpolicy.NewEvaluator([]netpolicy.Rule{
		{
			ID:          "rule-allow-echo",
			HostPattern: host,
			Protocol:    netpolicy.ProtocolTCP,
			Ports:       []int{port},
			Action:      netpolicy.ActionAllow,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	proxy, err := netpolicy.NewEgressProxy(netpolicy.ProxyConfig{
		Evaluator: engine,
	})
	if err != nil {
		t.Fatal(err)
	}
	proxy.Start()
	defer proxy.Close()

	// 3. Connect via CONNECT to proxy
	proxyConn, err := net.Dial("tcp", proxy.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer proxyConn.Close()

	// Send CONNECT request
	connectReq := fmt.Sprintf("CONNECT %s:%d HTTP/1.1\r\nHost: %s:%d\r\n\r\n", host, port, host, port)
	_, _ = proxyConn.Write([]byte(connectReq))

	br := bufio.NewReader(proxyConn)
	statusLine, err := br.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(statusLine, "200 Connection Established") {
		t.Fatalf("expected 200 Connection Established, got: %s", statusLine)
	}
	// Read empty line
	_, _ = br.ReadString('\n')

	// Send data through tunnel
	_, _ = proxyConn.Write([]byte("PING\n"))
	respLine, err := br.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(respLine) != "PONG" {
		t.Fatalf("expected PONG through tunnel, got: %s", respLine)
	}
}

func TestEgressProxyPrivateIPRejection(t *testing.T) {
	// A rule with a domain name must NOT allow traffic if that domain resolves to a private IP (anti-SSRF/rebinding)
	engine, err := netpolicy.NewEvaluator([]netpolicy.Rule{
		{
			ID:          "rule-allow-domain",
			HostPattern: "api.internal.com",
			Protocol:    netpolicy.ProtocolTCP,
			Ports:       []int{443},
			Action:      netpolicy.ActionAllow,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	proxy, err := netpolicy.NewEgressProxy(netpolicy.ProxyConfig{
		Evaluator: engine,
	})
	if err != nil {
		t.Fatal(err)
	}
	proxy.Start()
	defer proxy.Close()

	proxyConn, err := net.Dial("tcp", proxy.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer proxyConn.Close()

	// Try to CONNECT to metadata service 169.254.169.254
	connectReq := "CONNECT 169.254.169.254:443 HTTP/1.1\r\nHost: 169.254.169.254:443\r\n\r\n"
	_, _ = proxyConn.Write([]byte(connectReq))

	br := bufio.NewReader(proxyConn)
	statusLine, err := br.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(statusLine, "403") {
		t.Fatalf("expected 403 Forbidden for metadata IP, got: %s", statusLine)
	}
}
