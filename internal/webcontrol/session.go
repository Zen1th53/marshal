package webcontrol

import (
	"encoding/json"
	"net/http"
	"time"
)

type LoginRequestDTO struct {
	Code string `json:"code"`
}

type AuthUserDTO struct {
	PrincipalID string   `json:"principal_id"`
	Role        string   `json:"role"`
	Authorities []string `json:"authorities"`
}

func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", "")
		return
	}

	var req LoginRequestDTO
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Code == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "Missing or malformed one-time code", "")
		return
	}

	session, err := s.sessions.RedeemOneTimeCode(req.Code)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid_code", err.Error(), "")
		return
	}

	// Set HttpOnly, SameSite=Lax cookie
	cookie := &http.Cookie{
		Name:     SessionCookieName,
		Value:    session.ID,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   !s.IsLoopback(), // Secure on HTTPS / remote connections
		Expires:  session.ExpiresAt,
	}
	http.SetCookie(w, cookie)

	user := AuthUserDTO{
		PrincipalID: session.PrincipalID,
		Role:        session.Role,
		Authorities: []string{"task.plan", "source.write", "verify.qa", "verify.security", "release.approve", "policy.admin"},
	}

	writeJSON(w, http.StatusOK, user)
}

func (s *Server) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil || cookie.Value == "" {
		// In loopback development mode without explicit session, return loopback operator
		if s.IsLoopback() {
			user := AuthUserDTO{
				PrincipalID: "operator-loopback",
				Role:        "admin",
				Authorities: []string{"task.plan", "source.write", "verify.qa", "verify.security", "release.approve", "policy.admin"},
			}
			writeJSON(w, http.StatusOK, user)
			return
		}
		writeError(w, http.StatusUnauthorized, "unauthorized", "No active session cookie", "")
		return
	}

	session, err := s.sessions.GetSession(cookie.Value)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "session_expired", err.Error(), "")
		return
	}

	user := AuthUserDTO{
		PrincipalID: session.PrincipalID,
		Role:        session.Role,
		Authorities: []string{"task.plan", "source.write", "verify.qa", "verify.security", "release.approve", "policy.admin"},
	}

	writeJSON(w, http.StatusOK, user)
}

func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(SessionCookieName); err == nil && cookie.Value != "" {
		s.sessions.RevokeSession(cookie.Value)
	}

	// Clear session cookie
	clearCookie := &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   !s.IsLoopback(),
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
	}
	http.SetCookie(w, clearCookie)

	writeJSON(w, http.StatusOK, map[string]string{"status": "logged_out"})
}
