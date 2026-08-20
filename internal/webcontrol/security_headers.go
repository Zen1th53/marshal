package webcontrol

import "net/http"

// SecurityHeadersMiddleware applies CSP, HSTS, anti-framing, and MIME sniffing protection
func (s *Server) SecurityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()

		// Anti-framing / clickjacking defense
		h.Set("X-Frame-Options", "DENY")

		// Prevent MIME-sniffing
		h.Set("X-Content-Type-Options", "nosniff")

		// Referrer policy
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")

		// Content Security Policy (strict local-only assets, zero remote CDN)
		csp := "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; font-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self';"
		h.Set("Content-Security-Policy", csp)

		// Permissions-Policy
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")

		// HSTS on non-loopback / HTTPS
		if !s.IsLoopback() {
			h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
		}

		next.ServeHTTP(w, r)
	})
}
