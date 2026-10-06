package netpolicy

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type unusedListener struct{}

func (unusedListener) Accept() (net.Conn, error) { return nil, io.EOF }
func (unusedListener) Close() error              { return nil }
func (unusedListener) Addr() net.Addr            { return &net.UnixAddr{Name: "unused", Net: "unix"} }

func TestProxyRefusalEvidenceAndAlert(t *testing.T) {
	a, _ := NewRunAllowlist([]string{"api.example.com"})
	var attempts []Decision
	p, err := NewEgressProxy(ProxyConfig{Evaluator: a, Listener: unusedListener{}, Attempt: func(_ context.Context, host string, port int, d Decision) error {
		attempts = append(attempts, d)
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	for _, r := range []*http.Request{
		httptest.NewRequest("GET", "http://unapproved.example.com/", nil),
		httptest.NewRequest("CONNECT", "http://unapproved.example.com:443", nil),
		httptest.NewRequest("GET", "/malformed", nil),
	} {
		w := httptest.NewRecorder()
		p.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden && w.Code != http.StatusBadRequest {
			t.Fatalf("response %d", w.Code)
		}
	}
	if len(attempts) != 3 {
		t.Fatalf("missing attempt evidence: %+v", attempts)
	}
	for _, d := range attempts {
		if d.Allowed {
			t.Fatal("unexpected authority")
		}
	}
}

func TestProxyEvidenceFailureRefusesAllowedAttempt(t *testing.T) {
	a, _ := NewRunAllowlist([]string{"127.0.0.1:8080"})
	p, err := NewEgressProxy(ProxyConfig{Evaluator: a, Listener: unusedListener{}, Attempt: func(_ context.Context, _ string, _ int, d Decision) error {
		if !d.Allowed {
			t.Fatal("allowed endpoint not evaluated")
		}
		return errors.New("evidence unavailable")
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	w := httptest.NewRecorder()
	p.ServeHTTP(w, httptest.NewRequest("GET", "http://127.0.0.1:8080/", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("failed open: %d", w.Code)
	}
}

func TestProxyRevokeClosesOnlyExactEndpoint(t *testing.T) {
	a, _ := NewRunAllowlist(nil)
	p, err := NewEgressProxy(ProxyConfig{Evaluator: a, Listener: unusedListener{}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	conn, peer := net.Pipe()
	defer peer.Close()
	p.connections[conn] = "example.com:443"
	other, otherPeer := net.Pipe()
	defer otherPeer.Close()
	p.connections[other] = "example.com:8443"
	p.CloseEndpoint("other.example.com:443")
	p.CloseEndpoint("example.com:443")
	if _, err := peer.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("tunnel survived revoke: %v", err)
	}
	if err := otherPeer.SetReadDeadline(time.Now().Add(10 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	_, err = otherPeer.Read(make([]byte, 1))
	var timeout net.Error
	if !errors.As(err, &timeout) || !timeout.Timeout() {
		t.Fatalf("another port was revoked: %v", err)
	}
	p.disconnect(conn)
}

func TestGrantPersistenceFailureKeepsEndpointClosed(t *testing.T) {
	a, _ := NewRunAllowlist(nil)
	err := a.Set("example.com", true, func(string) error { return errors.New("disk full") })
	if err == nil {
		t.Fatal("grant succeeded without evidence")
	}
	d, _ := a.Evaluate(context.Background(), Request{Host: "example.com", Port: 443, Protocol: ProtocolTCP})
	if d.Allowed {
		t.Fatal("endpoint opened without evidence")
	}
	for _, endpoint := range a.Endpoints() {
		if strings.Contains(endpoint, "example.com") {
			t.Fatal("failed grant in snapshot")
		}
	}
}

func TestParserRefusalIsObserved(t *testing.T) {
	a, _ := NewRunAllowlist(nil)
	client, server := net.Pipe()
	ln := &oneConnectionListener{conn: server, closed: make(chan struct{})}
	refused := make(chan bool, 1)
	p, err := NewEgressProxy(ProxyConfig{Evaluator: a, Listener: ln, Attempt: func(_ context.Context, _ string, _ int, d Decision) error { refused <- !d.Allowed; return nil }})
	if err != nil {
		t.Fatal(err)
	}
	p.Start()
	defer p.Close()
	defer client.Close()
	client.SetDeadline(time.Now().Add(time.Second))
	go func() { client.Write([]byte("invalid request\r\n\r\n")) }()
	io.ReadAll(client)
	select {
	case denied := <-refused:
		if !denied {
			t.Fatal("parser allowed")
		}
	case <-time.After(time.Second):
		t.Fatal("parser refusal not observed")
	}
}

type oneConnectionListener struct {
	conn   net.Conn
	closed chan struct{}
	once   sync.Once
}

func (l *oneConnectionListener) Accept() (net.Conn, error) {
	if l.conn != nil {
		c := l.conn
		l.conn = nil
		return c, nil
	}
	<-l.closed
	return nil, net.ErrClosed
}
func (l *oneConnectionListener) Close() error   { l.once.Do(func() { close(l.closed) }); return nil }
func (l *oneConnectionListener) Addr() net.Addr { return &net.UnixAddr{Name: "test", Net: "unix"} }
