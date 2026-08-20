package webcontrol

import "net/http"

const (
	StrictCSPPolicy = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; font-src 'self'; img-src 'self' data:; connect-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'self'; form-action 'self';"
)

// SecurityHeadersMiddleware applies strict CSP, HSTS, anti-framing, cross-origin, and MIME sniffing protection
func (s *Server) SecurityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()

		// 1. Anti-framing / clickjacking defense
		h.Set("X-Frame-Options", "DENY")

		// 2. Prevent MIME-sniffing
		h.Set("X-Content-Type-Options", "nosniff")

		// 3. Referrer policy
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")

		// 4. Strict Content Security Policy
		h.Set("Content-Security-Policy", StrictCSPPolicy)

		// 5. Permissions-Policy
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")

		// 6. Cross-Origin Isolation Policies
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")

		// 7. HSTS on non-loopback / HTTPS
		if !s.IsLoopback() {
			h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
		}

		next.ServeHTTP(w, r)
	})
}
