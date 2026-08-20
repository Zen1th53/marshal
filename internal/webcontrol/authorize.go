package webcontrol

import (
	"errors"
	"net/http"
	"strings"
)

var (
	ErrForbidden = errors.New("forbidden: insufficient authority for requested action")
	ErrConflict  = errors.New("conflict: stale revision detected (concurrency violation)")
)

// RequiredAuthority specifies the required authority token for an operation
func (s *Server) RequireAuthority(authority string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 1. Get session from cookie or loopback default
		user := s.getAuthenticatedUser(r)
		if user == nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "Valid authentication session required", "")
			return
		}

		// 2. Admin role has universal authority
		if user.Role == "admin" {
			next(w, r)
			return
		}

		// 3. Viewer role is strictly read-only
		if user.Role == "viewer" {
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				writeError(w, http.StatusForbidden, "forbidden", "Viewer role is not authorized to perform state mutations", "")
				return
			}
		}

		// 4. Verify specific authority
		hasAuthority := false
		for _, auth := range user.Authorities {
			if auth == authority || auth == "*" {
				hasAuthority = true
				break
			}
		}

		if !hasAuthority {
			writeError(w, http.StatusForbidden, "insufficient_authority", "Operation requires authority: "+authority, "")
			return
		}

		next(w, r)
	}
}

// RequireAuth requires only a valid authenticated session (any role), and is
// used for read-only endpoints that a viewer may access.
func (s *Server) RequireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.getAuthenticatedUser(r) == nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "Valid authentication session required", "")
			return
		}
		next(w, r)
	}
}

func (s *Server) getAuthenticatedUser(r *http.Request) *AuthUserDTO {
	cookie, err := r.Cookie(SessionCookieName)
	if err == nil && cookie.Value != "" {
		session, err := s.sessions.GetSession(cookie.Value)
		if err == nil && session != nil {
			authorities := s.getAuthoritiesForRole(session.Role)
			return &AuthUserDTO{
				PrincipalID: session.PrincipalID,
				Role:        session.Role,
				Authorities: authorities,
			}
		}
	}
	// Anonymous requests have NO privileged authority (even on loopback).
	// Callers must establish an authenticated session.
	return nil
}

func (s *Server) getAuthoritiesForRole(role string) []string {
	switch strings.ToLower(role) {
	case "admin":
		return []string{"task.plan", "source.write", "verify.qa", "verify.security", "release.approve", "policy.admin"}
	case "operator", "orchestrator", "architect", "developer":
		return []string{"task.plan", "source.write", "verify.qa"}
	case "security_officer", "appsec":
		return []string{"verify.security", "release.approve", "policy.admin"}
	case "qa", "qa_lead":
		return []string{"verify.qa", "task.plan"}
	case "viewer":
		return []string{}
	default:
		return []string{}
	}
}

// ValidateRevision performs optimistic concurrency control (CAS) check
func ValidateRevision(currentRevision, expectedRevision int) error {
	if expectedRevision > 0 && currentRevision != expectedRevision {
		return ErrConflict
	}
	return nil
}
