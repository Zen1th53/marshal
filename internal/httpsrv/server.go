package httpsrv

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"
)

const (
	DefaultReadHeaderTimeout = 5 * time.Second
	DefaultReadTimeout       = 30 * time.Second
	DefaultWriteTimeout      = 60 * time.Second
	DefaultIdleTimeout       = 120 * time.Second
	DefaultMaxHeaderBytes    = 1 << 20 // 1MB
	DefaultMaxBodyBytes      = 2 << 20 // 2MB
)

type Config struct {
	Addr              string
	Handler           http.Handler
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	MaxHeaderBytes    int
	MaxBodyBytes      int64
}

type Server struct {
	httpServer *http.Server
	maxBody    int64
}

func NewServer(cfg Config) *Server {
	readHeaderTimeout := cfg.ReadHeaderTimeout
	if readHeaderTimeout == 0 {
		readHeaderTimeout = DefaultReadHeaderTimeout
	}
	readTimeout := cfg.ReadTimeout
	if readTimeout == 0 {
		readTimeout = DefaultReadTimeout
	}
	writeTimeout := cfg.WriteTimeout
	if writeTimeout == 0 {
		writeTimeout = DefaultWriteTimeout
	}
	idleTimeout := cfg.IdleTimeout
	if idleTimeout == 0 {
		idleTimeout = DefaultIdleTimeout
	}
	maxHeaderBytes := cfg.MaxHeaderBytes
	if maxHeaderBytes == 0 {
		maxHeaderBytes = DefaultMaxHeaderBytes
	}
	maxBodyBytes := cfg.MaxBodyBytes
	if maxBodyBytes == 0 {
		maxBodyBytes = DefaultMaxBodyBytes
	}

	handler := LimitBodyMiddleware(cfg.Handler, maxBodyBytes)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
	}

	return &Server{
		httpServer: srv,
		maxBody:    maxBodyBytes,
	}
}

func LimitBodyMiddleware(next http.Handler, maxBytes int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) HTTPServer() *http.Server {
	return s.httpServer
}

func (s *Server) Serve(l net.Listener) error {
	err := s.httpServer.Serve(l)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) ListenAndServe() error {
	err := s.httpServer.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) Close() error {
	return s.httpServer.Close()
}
