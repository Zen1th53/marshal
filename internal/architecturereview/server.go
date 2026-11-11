package architecturereview

import (
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
)

//go:embed review.html
var reviewHTML string

func (r *Review) Handler(token string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		reply := func(status int, value any) {
			body, err := canonicalJSON(value)
			if err != nil {
				status = http.StatusInternalServerError
				body = json.RawMessage(`{"error":"review response could not be encoded"}`)
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(status)
			_, _ = w.Write(body)
		}
		host := q.Host
		if parsed, _, err := net.SplitHostPort(host); err == nil {
			host = parsed
		}
		ip := net.ParseIP(host)
		if len(token) < 16 || (host != "localhost" && (ip == nil || !ip.IsLoopback())) {
			reply(http.StatusForbidden, map[string]any{"error": "Local review access denied"})
			return
		}
		matches := func(actual string) bool { return subtle.ConstantTimeCompare([]byte(actual), []byte(token)) == 1 }
		if q.Method == http.MethodGet && q.URL.Path == "/" && matches(q.URL.Query().Get("token")) {
			encoded, _ := json.Marshal(token)
			page := strings.ReplaceAll(reviewHTML, "__REVIEW_TOKEN__", string(encoded))
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; img-src data:")
			_, _ = io.WriteString(w, page)
			return
		}
		if (q.URL.Path != "/api/review" && q.URL.Path != "/api/builders") || !matches(strings.TrimPrefix(q.Header.Get("Authorization"), "Bearer ")) || !strings.HasPrefix(q.Header.Get("Authorization"), "Bearer ") {
			reply(http.StatusForbidden, map[string]any{"error": "Review access denied"})
			return
		}
		if q.URL.Path == "/api/builders" {
			if q.Method != http.MethodGet {
				reply(http.StatusMethodNotAllowed, map[string]any{"error": "Unsupported review method"})
				return
			}
			r.mu.Lock()
			options := r.builderOptions(q.Context())
			r.mu.Unlock()
			list := []any{}
			if options != nil {
				list = append(list, map[string]any{"id": "auto", "glyph": "", "available": true})
				for _, option := range options {
					entry := map[string]any{"id": option.ID, "glyph": option.Glyph, "available": option.Available}
					if option.Limit != "" {
						entry["limit"] = option.Limit
					}
					if option.Reason != "" {
						entry["reason"] = option.Reason
					}
					list = append(list, entry)
				}
			}
			reply(http.StatusOK, map[string]any{"supported": options != nil, "options": list})
			return
		}
		if q.Method == http.MethodGet {
			state, err := r.State()
			if err != nil {
				reply(http.StatusInternalServerError, map[string]any{"error": "Review state unavailable"})
				return
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_, _ = w.Write(state)
			return
		}
		if q.Method != http.MethodPost {
			reply(http.StatusMethodNotAllowed, map[string]any{"error": "Unsupported review method"})
			return
		}
		if origin := q.Header.Get("Origin"); origin != "" && origin != "http://"+q.Host {
			reply(http.StatusForbidden, map[string]any{"error": "Review origin differs"})
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, q.Body, 1_000_000))
		if err != nil || len(body) == 0 {
			reply(http.StatusBadRequest, map[string]any{"error": "Review request body is invalid or too large"})
			return
		}
		state, err := r.Apply(q.Context(), body)
		if err != nil {
			reply(http.StatusConflict, map[string]any{"error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write(state)
	})
}
