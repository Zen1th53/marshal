package api

import (
	"context"
	"encoding/json"
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

// Exercise real HTTP connection deadlines with a slow stand-in for a worker.
// Scaling the server timeout keeps the regression fast while crossing it by 4x.
func TestLongRunResponseAndOrdinaryRouteTimeout(t *testing.T) {
	s := NewServer(nil)
	slowWorker := func(context.Context, *http.Request) (any, error) {
		time.Sleep(200 * time.Millisecond)
		return "worker finished", nil
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/tasks/{id}/run", s.handleRun(slowWorker))
	mux.HandleFunc("POST /ordinary", s.handle(slowWorker))
	listener := &pipeListener{connections: make(chan net.Conn), done: make(chan struct{})}
	server := &http.Server{Handler: mux, WriteTimeout: 50 * time.Millisecond}
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Serve(listener) }()
	defer func() {
		server.Close()
		<-serverDone
	}()
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		clientConn, serverConn := net.Pipe()
		select {
		case listener.connections <- serverConn:
			return clientConn, nil
		case <-ctx.Done():
			clientConn.Close()
			serverConn.Close()
			return nil, ctx.Err()
		}
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	response, err := client.Post("http://runtime/v1/tasks/task/run", "application/json", nil)
	if err != nil {
		t.Fatalf("long run response lost: %v", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusOK || !strings.Contains(string(data), "worker finished") {
		t.Fatalf("long run response: status=%d body=%s err=%v", response.StatusCode, data, err)
	}
	// Reuse the same client/connection: the exemption must not leak to another route.
	response, err = client.Post("http://runtime/ordinary", "application/json", nil)
	if err == nil {
		defer response.Body.Close()
		data, err = io.ReadAll(response.Body)
		if err == nil {
			t.Fatalf("ordinary route escaped its timeout: %s", data)
		}
	}
	if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("ordinary response failed for a reason other than the server closing it: %v", err)
	}
}

type deadlineRecorder struct {
	*httptest.ResponseRecorder
	deadlines []time.Time
}

func (w *deadlineRecorder) SetWriteDeadline(deadline time.Time) error {
	w.deadlines = append(w.deadlines, deadline)
	return nil
}

func TestOnlyRunRouteSuspendsDeadlineAndRestoresResponseLimit(t *testing.T) {
	for _, path := range []string{"/v1/tasks/task/run", "/v1/tasks/task/claim", "/v1/version"} {
		t.Run(path, func(t *testing.T) {
			writer := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
			method := http.MethodPost
			if path == "/v1/version" {
				method = http.MethodGet
			}
			// Malformed JSON avoids starting work while checking the real route wiring
			// and verifies even a run error restores the response write limit.
			NewServer(nil).routes().ServeHTTP(writer, httptest.NewRequest(method, path, strings.NewReader("{")))
			if path != "/v1/tasks/task/run" {
				if len(writer.deadlines) != 0 {
					t.Fatalf("ordinary route changed deadlines: %v", writer.deadlines)
				}
				return
			}
			if len(writer.deadlines) != 2 || !writer.deadlines[0].IsZero() {
				t.Fatalf("run deadlines = %v", writer.deadlines)
			}
			remaining := time.Until(writer.deadlines[1])
			if remaining <= 0 || remaining > responseWriteTimeout {
				t.Fatalf("response deadline remaining = %v", remaining)
			}
			var envelope Envelope
			if err := json.Unmarshal(writer.Body.Bytes(), &envelope); err != nil || envelope.Error == nil {
				t.Fatalf("expected bounded run error response: %s (%v)", writer.Body.Bytes(), err)
			}
		})
	}
}

// net.Pipe exercises net/http deadlines without host listener privileges.
type pipeListener struct {
	connections chan net.Conn
	done        chan struct{}
	closeOnce   sync.Once
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.connections:
		return conn, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}
func (l *pipeListener) Close() error   { l.closeOnce.Do(func() { close(l.done) }); return nil }
func (l *pipeListener) Addr() net.Addr { return pipeAddress{} }

type pipeAddress struct{}

func (pipeAddress) Network() string { return "pipe" }
func (pipeAddress) String() string  { return "runtime" }
