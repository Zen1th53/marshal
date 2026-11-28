package architecturereview

import (
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/Zen1th53/marshal/internal/architecturecodec"
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
			body, err := json.Marshal(value)
			if err != nil {
				status = http.StatusInternalServerError
				body = json.RawMessage(`{"error":"review response could not be encoded"}`)
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(status)
			_, _ = w.Write(body)
		}
		// Only the default view and the roomy renderer are supported.
		for _, layout := range q.URL.Query()["layout"] {
			if layout != "" && layout != "roomy" {
				reply(http.StatusBadRequest, map[string]any{"error": "Unsupported layout"})
				return
			}
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
		if (q.URL.Path != "/api/roomy" && q.URL.Path != "/api/review" && q.URL.Path != "/api/builders" && q.URL.Path != "/api/design" && q.URL.Path != "/api/history" && q.URL.Path != "/api/conformance" && q.URL.Path != "/api/codeview") || !matches(strings.TrimPrefix(q.Header.Get("Authorization"), "Bearer ")) || !strings.HasPrefix(q.Header.Get("Authorization"), "Bearer ") {
			reply(http.StatusForbidden, map[string]any{"error": "Review access denied"})
			return
		}
		if q.URL.Path == "/api/roomy" {
			if q.Method != http.MethodPost {
				reply(http.StatusMethodNotAllowed, map[string]any{"error": "Unsupported renderer method"})
				return
			}
			raw, err := io.ReadAll(http.MaxBytesReader(w, q.Body, 8<<20))
			if err != nil {
				reply(http.StatusBadRequest, map[string]any{"error": "Architecture is too large"})
				return
			}
			svg, err := architecturecodec.RenderSVG(raw, architecturecodec.RendererOptions{Roomy: true})
			if err != nil {
				reply(http.StatusBadRequest, map[string]any{"error": err.Error()})
				return
			}
			reply(http.StatusOK, map[string]any{"svg": svg})
			return
		}
		if q.URL.Path == "/api/codeview" {
			if q.Method != http.MethodGet {
				reply(http.StatusMethodNotAllowed, map[string]any{"error": "Unsupported review method"})
				return
			}
			view, err := r.CodeView(q.Context(), q.URL.Query().Get("part"), q.URL.Query().Get("dir"))
			if err != nil {
				reply(http.StatusNotFound, map[string]any{"error": err.Error()})
				return
			}
			reply(http.StatusOK, view)
			return
		}
		if q.URL.Path == "/api/conformance" {
			if q.Method != http.MethodGet {
				reply(http.StatusMethodNotAllowed, map[string]any{"error": "Unsupported review method"})
				return
			}
			if r.config.Conformance == nil {
				reply(http.StatusOK, map[string]any{"present": false})
				return
			}
			value, err := r.config.Conformance(q.Context())
			if err != nil {
				reply(http.StatusServiceUnavailable, map[string]any{"error": err.Error()})
				return
			}
			reply(http.StatusOK, value)
			return
		}
		if q.URL.Path == "/api/history" {
			if q.Method != http.MethodGet {
				reply(http.StatusMethodNotAllowed, map[string]any{"error": "Unsupported review method"})
				return
			}
			revision := int64(0)
			if value := q.URL.Query().Get("revision"); value != "" {
				var err error
				revision, err = strconv.ParseInt(value, 10, 64)
				if err != nil || revision < 1 {
					reply(http.StatusBadRequest, map[string]any{"error": "Revision must be a positive number"})
					return
				}
			}
			if r.config.History == nil {
				reply(http.StatusOK, map[string]any{"revisions": []any{}})
				return
			}
			value, err := r.config.History(q.Context(), revision)
			if err != nil {
				reply(http.StatusNotFound, map[string]any{"error": err.Error()})
				return
			}
			reply(http.StatusOK, value)
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
		if q.URL.Path == "/api/design" {
			if q.Method != http.MethodGet {
				reply(http.StatusMethodNotAllowed, map[string]any{"error": "Unsupported review method"})
				return
			}
			r.mu.Lock()
			object, digest, err := r.design(q.Context())
			r.mu.Unlock()
			// Hostile or unreadable design input is never shown; the page sees
			// only a design that is absent.
			if err != nil {
				reply(http.StatusOK, map[string]any{"present": false})
				return
			}
			response := map[string]any{}
			for key, value := range object {
				response[key] = value
			}
			if digest != "" {
				response["digest"] = digest
			}
			reply(http.StatusOK, response)
			return
		}
		if q.Method == http.MethodGet {
			state, err := r.State()
			if err != nil {
				reply(http.StatusInternalServerError, map[string]any{"error": "Review state unavailable"})
				return
			}
			reply(http.StatusOK, state)
			return
		}
		if q.Method != http.MethodPost {
			reply(http.StatusMethodNotAllowed, map[string]any{"error": "Unsupported review method"})
			return
		}
		if r.config.ReadOnly {
			reply(http.StatusConflict, map[string]any{"error": "This is the accepted architecture. Submit a new project revision to change it."})
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
		reply(http.StatusOK, state)
	})
}
