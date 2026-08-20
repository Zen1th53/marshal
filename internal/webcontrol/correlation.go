package webcontrol

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"regexp"
	"time"
)

type correlationKey struct{}

var safeCorrelationIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_\-\.]{8,64}$`)

func NewCorrelationID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("req-%d-%s", time.Now().UnixMilli(), hex.EncodeToString(b))
}

func SanitizeCorrelationID(raw string) string {
	if raw != "" && safeCorrelationIDPattern.MatchString(raw) {
		return raw
	}
	return NewCorrelationID()
}

func WithCorrelationID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, correlationKey{}, id)
}

func GetCorrelationID(ctx context.Context) string {
	if val, ok := ctx.Value(correlationKey{}).(string); ok && val != "" {
		return val
	}
	return ""
}

func (s *Server) CorrelationMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := r.Header.Get("X-Correlation-ID")
		correlationID := SanitizeCorrelationID(raw)

		w.Header().Set("X-Correlation-ID", correlationID)
		ctx := WithCorrelationID(r.Context(), correlationID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
