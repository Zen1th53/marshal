package httpsrv

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestHTTPServerTimeoutsAndLimits(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("pong"))
	})

	srv := NewServer(Config{
		Addr:              "127.0.0.1:0",
		Handler:           mux,
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       10 * time.Second,
		MaxHeaderBytes:    64 * 1024,
		MaxBodyBytes:      1024,
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(ln)
	}()

	addr := ln.Addr().String()

	// 1. Normal GET request -> 200 OK
	resp, err := http.Get("http://" + addr + "/ping")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "pong" {
		t.Fatalf("expected 'pong', got %q", string(body))
	}

	// 2. Graceful Shutdown
	shutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		t.Fatalf("Shutdown failed: %v", err)
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("unexpected Serve error on shutdown: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop in time after shutdown")
	}
}

func TestHTTPServerOversizedBodyRejection(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				w.WriteHeader(http.StatusRequestEntityTooLarge)
				_, _ = w.Write([]byte("too large"))
				return
			}
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	})

	srv := NewServer(Config{
		Addr:         "127.0.0.1:0",
		Handler:      mux,
		MaxBodyBytes: 100, // 100 bytes max
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		_ = srv.Serve(ln)
	}()
	defer func() { _ = srv.Close() }()

	addr := ln.Addr().String()

	// 1. Small body within limit -> 200 OK
	smallPayload := bytes.Repeat([]byte("a"), 50)
	resp, err := http.Post("http://"+addr+"/echo", "application/octet-stream", bytes.NewReader(smallPayload))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK for small payload, got %d", resp.StatusCode)
	}

	// 2. Large body exceeding limit -> 413 Request Entity Too Large
	largePayload := bytes.Repeat([]byte("a"), 500)
	resp, err = http.Post("http://"+addr+"/echo", "application/octet-stream", bytes.NewReader(largePayload))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 for oversized payload, got %d", resp.StatusCode)
	}
}
