package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
)

const maxRequestBody = 1 << 20

type Server struct {
	runtime *app.Runtime
}

func NewServer(runtime *app.Runtime) *Server { return &Server{runtime: runtime} }

func (s *Server) Serve(ctx context.Context, socketPath string) error {
	if s.runtime == nil || socketPath == "" {
		return fmt.Errorf("%w: runtime and socket path are required", model.ErrInvalid)
	}
	if err := prepareSocket(socketPath); err != nil {
		return err
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("listen on runtime socket: %w", err)
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		listener.Close()
		os.Remove(socketPath)
		return fmt.Errorf("secure runtime socket: %w", err)
	}
	server := &http.Server{
		Handler:           s.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	stopped := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = server.Shutdown(shutdownCtx)
		case <-stopped:
		}
	}()
	err = server.Serve(listener)
	close(stopped)
	_ = listener.Close()
	_ = os.Remove(socketPath)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/version", s.handle(func(ctx context.Context, _ *http.Request) (any, error) {
		return Version{APIVersion: "v1"}, nil
	}))
	mux.HandleFunc("GET /v1/health", s.handle(func(ctx context.Context, _ *http.Request) (any, error) {
		if _, err := s.runtime.Status(ctx); err != nil {
			return nil, err
		}
		return Health{Status: "ready"}, nil
	}))
	mux.HandleFunc("GET /v1/status", s.handle(func(ctx context.Context, _ *http.Request) (any, error) {
		return s.runtime.Status(ctx)
	}))
	mux.HandleFunc("POST /v1/agents", s.handle(func(ctx context.Context, request *http.Request) (any, error) {
		var input app.RegisterAgentRequest
		if err := decode(request, &input); err != nil {
			return nil, err
		}
		return s.runtime.RegisterAgent(ctx, input)
	}))
	mux.HandleFunc("GET /v1/agents", s.handle(func(ctx context.Context, _ *http.Request) (any, error) {
		return s.runtime.Agents(ctx)
	}))
	mux.HandleFunc("POST /v1/tasks/import", s.handle(func(ctx context.Context, request *http.Request) (any, error) {
		var tasks []model.Task
		if err := decode(request, &tasks); err != nil {
			return nil, err
		}
		return s.runtime.ImportTasks(ctx, tasks)
	}))
	mux.HandleFunc("GET /v1/tasks", s.handle(func(ctx context.Context, _ *http.Request) (any, error) {
		return s.runtime.Tasks(ctx)
	}))
	mux.HandleFunc("GET /v1/tasks/{id}", s.handle(func(ctx context.Context, request *http.Request) (any, error) {
		return s.runtime.Task(ctx, request.PathValue("id"))
	}))
	mux.HandleFunc("POST /v1/tasks/{id}/claim", s.handle(func(ctx context.Context, request *http.Request) (any, error) {
		var input app.ClaimRequest
		if err := decode(request, &input); err != nil {
			return nil, err
		}
		if input.TaskID != "" && input.TaskID != request.PathValue("id") {
			return nil, fmt.Errorf("%w: task path and body differ", model.ErrInvalid)
		}
		input.TaskID = request.PathValue("id")
		return s.runtime.Claim(ctx, input)
	}))
	mux.HandleFunc("POST /v1/tasks/{id}/release", s.handle(func(ctx context.Context, request *http.Request) (any, error) {
		var input app.ReleaseRequest
		if err := decode(request, &input); err != nil {
			return nil, err
		}
		if input.TaskID != "" && input.TaskID != request.PathValue("id") {
			return nil, fmt.Errorf("%w: task path and body differ", model.ErrInvalid)
		}
		input.TaskID = request.PathValue("id")
		return struct{}{}, s.runtime.Release(ctx, input)
	}))
	mux.HandleFunc("POST /v1/tasks/{id}/run", s.handle(func(ctx context.Context, request *http.Request) (any, error) {
		var input app.RunRequest
		if err := decode(request, &input); err != nil {
			return nil, err
		}
		if input.TaskID != "" && input.TaskID != request.PathValue("id") {
			return nil, fmt.Errorf("%w: task path and body differ", model.ErrInvalid)
		}
		input.TaskID = request.PathValue("id")
		return s.runtime.Run(ctx, input)
	}))
	mux.HandleFunc("POST /v1/tasks/{id}/cancel", s.handle(func(ctx context.Context, request *http.Request) (any, error) {
		return struct{}{}, s.runtime.CancelTask(ctx, request.PathValue("id"))
	}))
	mux.HandleFunc("GET /v1/events", s.handle(func(ctx context.Context, _ *http.Request) (any, error) {
		return s.runtime.Events(ctx)
	}))
	mux.HandleFunc("GET /v1/artifacts", s.handle(func(ctx context.Context, _ *http.Request) (any, error) {
		return s.runtime.Artifacts(ctx)
	}))
	mux.HandleFunc("POST /v1/verify", s.handle(func(ctx context.Context, request *http.Request) (any, error) {
		var input app.VerifyRequest
		if err := decode(request, &input); err != nil {
			return nil, err
		}
		return s.runtime.Verify(ctx, input)
	}))
	mux.HandleFunc("POST /v1/reconcile", s.handle(func(ctx context.Context, request *http.Request) (any, error) {
		var input app.ReconcileRequest
		if err := decode(request, &input); err != nil {
			return nil, err
		}
		return s.runtime.Reconcile(ctx, input)
	}))
	return mux
}

type handler func(context.Context, *http.Request) (any, error)

func (s *Server) handle(next handler) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		requestID, err := model.NewID("REQ-")
		if err != nil {
			writeError(writer, "", err)
			return
		}
		writer.Header().Set("X-Request-ID", requestID)
		result, err := next(request.Context(), request)
		if err != nil {
			writeError(writer, requestID, err)
			return
		}
		writeJSON(writer, http.StatusOK, requestID, result)
	}
}

func decode(request *http.Request, target any) error {
	reader := io.LimitReader(request.Body, maxRequestBody+1)
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%w: malformed JSON: %v", model.ErrInvalid, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("%w: trailing JSON", model.ErrInvalid)
	}
	return nil
}

func writeJSON(writer http.ResponseWriter, status int, requestID string, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		writeError(writer, requestID, err)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(Envelope{RequestID: requestID, Data: data})
}

func writeError(writer http.ResponseWriter, requestID string, err error) {
	status, code := errorStatus(err)
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(Envelope{RequestID: requestID, Error: &Error{Code: code, Message: err.Error()}})
}

func errorStatus(err error) (int, string) {
	switch {
	case errors.Is(err, model.ErrInvalid):
		return http.StatusBadRequest, "invalid_input"
	case errors.Is(err, model.ErrNotFound):
		return http.StatusNotFound, "not_found"
	case errors.Is(err, model.ErrConflict):
		return http.StatusConflict, "conflict"
	case errors.Is(err, model.ErrPolicyDenied):
		return http.StatusForbidden, "policy_denied"
	case errors.Is(err, model.ErrApprovalRequired):
		return http.StatusPreconditionRequired, "approval_required"
	case errors.Is(err, model.ErrUnavailable):
		return http.StatusServiceUnavailable, "unavailable"
	default:
		return http.StatusInternalServerError, "internal"
	}
}

func prepareSocket(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create socket directory: %w", err)
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect runtime socket: %w", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("%w: refusing to replace non-socket path", model.ErrConflict)
	}
	connection, dialErr := net.DialTimeout("unix", path, 200*time.Millisecond)
	if dialErr == nil {
		connection.Close()
		return fmt.Errorf("%w: runtime socket is live", model.ErrConflict)
	}
	if !errors.Is(dialErr, syscall.ECONNREFUSED) && !errors.Is(dialErr, os.ErrNotExist) {
		return fmt.Errorf("%w: runtime socket state is not positively stale: %v", model.ErrConflict, dialErr)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove stale runtime socket: %w", err)
	}
	return nil
}

func remoteError(value *Error) error {
	if value == nil {
		return nil
	}
	var sentinel error
	switch strings.ToLower(value.Code) {
	case "invalid_input":
		sentinel = model.ErrInvalid
	case "not_found":
		sentinel = model.ErrNotFound
	case "conflict":
		sentinel = model.ErrConflict
	case "policy_denied":
		sentinel = model.ErrPolicyDenied
	case "approval_required":
		sentinel = model.ErrApprovalRequired
	case "unavailable":
		sentinel = model.ErrUnavailable
	default:
		sentinel = errors.New("runtime API error")
	}
	return fmt.Errorf("%w: %s", sentinel, value.Message)
}
