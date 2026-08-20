package webcontrol

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

var (
	ErrNonLoopbackInsecure = errors.New("binding to non-loopback interface requires authentication and explicit secure configuration")
)

type ServerConfig struct {
	Host                     string
	Port                     int
	AllowInsecureNonLoopback bool
	ReadTimeout              time.Duration
	WriteTimeout             time.Duration
}

type Server struct {
	config   ServerConfig
	handler  http.Handler
	runtime  any
	sessions *SessionStore
	sseHub   *SSEHub
	mu       sync.RWMutex
}

func (s *Server) Sessions() *SessionStore {
	return s.sessions
}

func (s *Server) SSEHub() *SSEHub {
	return s.sseHub
}

func NewServer(cfg ServerConfig, runtime any) (*Server, error) {
	if cfg.Host == "" {
		cfg.Host = "127.0.0.1"
	}
	if cfg.Port <= 0 {
		cfg.Port = 8787
	}
	if cfg.ReadTimeout <= 0 {
		cfg.ReadTimeout = 15 * time.Second
	}
	if cfg.WriteTimeout <= 0 {
		cfg.WriteTimeout = 30 * time.Second
	}

	ip := net.ParseIP(cfg.Host)
	isLoopback := ip != nil && ip.IsLoopback()
	if !isLoopback && !cfg.AllowInsecureNonLoopback {
		return nil, fmt.Errorf("%w: host=%s", ErrNonLoopbackInsecure, cfg.Host)
	}

	s := &Server{
		config:   cfg,
		runtime:  runtime,
		sessions: NewSessionStore(),
		sseHub:   NewSSEHub(),
	}

	mux := http.NewServeMux()
	s.registerRoutes(mux)
	s.handler = s.SecurityHeadersMiddleware(s.CSRFMiddleware(s.CorrelationMiddleware(s.wrapMiddleware(mux))))

	return s, nil
}

func (s *Server) IsLoopback() bool {
	ip := net.ParseIP(s.config.Host)
	return ip != nil && ip.IsLoopback()
}

func (s *Server) Handler() http.Handler {
	return s.handler
}

func (s *Server) wrapMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		correlationID := GetCorrelationID(r.Context())
		if correlationID == "" {
			correlationID = NewCorrelationID()
		}
		w.Header().Set("X-Correlation-ID", correlationID)
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")

		// Propagate context
		ctx := r.Context()
		select {
		case <-ctx.Done():
			writeError(w, http.StatusRequestTimeout, "request_timeout", "Request timed out or client canceled", correlationID)
			return
		default:
			next.ServeHTTP(w, r)
		}
	})
}

type ErrorEnvelope struct {
	Error ErrorDetail `json:"error"`
}

type ErrorDetail struct {
	Code          string `json:"code"`
	Message       string `json:"message"`
	CorrelationID string `json:"correlation_id,omitempty"`
}

func writeError(w http.ResponseWriter, status int, code, message, correlationID string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(ErrorEnvelope{
		Error: ErrorDetail{
			Code:          code,
			Message:       message,
			CorrelationID: correlationID,
		},
	})
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
