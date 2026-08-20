package webcontrol

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

var (
	ErrCSRFTokenMissing = errors.New("missing CSRF token in X-CSRF-Token header")
	ErrCSRFTokenInvalid = errors.New("invalid or mismatched CSRF token")
	ErrOriginInvalid    = errors.New("cross-origin request denied: Origin header does not match Host")
	ErrHostInvalid      = errors.New("unauthorized Host header")
)

const (
	CSRFCookieName = "marshal_csrf"
	CSRFHeaderName = "X-CSRF-Token"
)

func GenerateCSRFToken(sessionID string, secretKey []byte) string {
	mac := hmac.New(sha256.New, secretKey)
	mac.Write([]byte(sessionID))
	return hex.EncodeToString(mac.Sum(nil))
}

func ValidateCSRFToken(token, sessionID string, secretKey []byte) bool {
	if token == "" || sessionID == "" || len(secretKey) == 0 {
		return false
	}
	expected := GenerateCSRFToken(sessionID, secretKey)
	return subtle.ConstantTimeCompare([]byte(token), []byte(expected)) == 1
}

// CSRFMiddleware validates Origin, Host, and CSRF token for state-changing requests
func (s *Server) CSRFMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := r.Method
		isSafeMethod := method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions

		// 1. Host header validation
		host := r.Host
		if host == "" {
			writeError(w, http.StatusBadRequest, "invalid_host", "Missing Host header", "")
			return
		}

		// 2. Origin validation for state-changing methods
		if !isSafeMethod {
			origin := r.Header.Get("Origin")
			if origin != "" {
				parsedOrigin, err := url.Parse(origin)
				if err != nil || (parsedOrigin.Host != host && !s.isAllowedOrigin(parsedOrigin.Host)) {
					writeError(w, http.StatusForbidden, "invalid_origin", "Cross-origin state mutation rejected", "")
					return
				}
			}

			// Exemption for initial login endpoint (establishing session)
			if r.URL.Path != "/api/v1/auth/login" {
				sessionCookie, err := r.Cookie(SessionCookieName)
				if err != nil || sessionCookie.Value == "" {
					writeError(w, http.StatusUnauthorized, "unauthorized", "Authentication session required for state changes", "")
					return
				}

				session, err := s.sessions.GetSession(sessionCookie.Value)
				if err != nil || session == nil {
					writeError(w, http.StatusUnauthorized, "session_expired", "Invalid or expired session", "")
					return
				}

				// Check CSRF token header
				csrfToken := r.Header.Get(CSRFHeaderName)
				if csrfToken == "" {
					writeError(w, http.StatusForbidden, "csrf_missing", "Missing X-CSRF-Token header for mutation", "")
					return
				}

				if !ValidateCSRFToken(csrfToken, sessionCookie.Value, s.csrfSecret) {
					writeError(w, http.StatusForbidden, "csrf_invalid", "Invalid or mismatched CSRF token", "")
					return
				}
			}
		}

		next.ServeHTTP(w, r)
	})
}

func (s *Server) isAllowedOrigin(originHost string) bool {
	if s.IsLoopback() {
		// Allow standard loopback variations in development
		return strings.HasPrefix(originHost, "127.0.0.1") ||
			strings.HasPrefix(originHost, "localhost") ||
			strings.HasPrefix(originHost, "[::1]")
	}
	return false
}

func (s *Server) handleGetCSRFToken(w http.ResponseWriter, r *http.Request) {
	sessionCookie, err := r.Cookie(SessionCookieName)
	if err != nil || sessionCookie.Value == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Session required to obtain CSRF token", "")
		return
	}

	session, err := s.sessions.GetSession(sessionCookie.Value)
	if err != nil || session == nil {
		writeError(w, http.StatusUnauthorized, "session_expired", "Invalid or expired session", "")
		return
	}

	token := GenerateCSRFToken(session.ID, s.csrfSecret)
	w.Header().Set(CSRFHeaderName, token)

	// Set double-submit CSRF cookie (readable by frontend)
	http.SetCookie(w, &http.Cookie{
		Name:     CSRFCookieName,
		Value:    token,
		Path:     "/",
		SameSite: http.SameSiteLaxMode,
		Secure:   !s.IsLoopback(),
	})

	writeJSON(w, http.StatusOK, map[string]string{
		"csrf_token": token,
	})
}
