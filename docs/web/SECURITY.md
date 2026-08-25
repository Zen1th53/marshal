# MARSHAL Web Control Plane — Security Architecture & Hardening Guide

**Security Baseline:** `1.0.1`

**Task:** T175  

This document details the security model, defense-in-depth layers, headers, and invariants enforced across the MARSHAL Web Control Plane.

---

## 1. Core Security Invariants

1. **Browser Never Touches SQLite Directly:** All state access and mutations cross canonical Go runtime authorization boundaries (`internal/authz` and `internal/webcontrol`).
2. **Zero Client Secret Storage:** Authentication tokens, API keys, private keys, and signing keys are never persisted to `localStorage`, `sessionStorage`, cookies, DOM attributes, or JavaScript bundles.
3. **Strict Content Security Policy (CSP):**
   - Directives: `default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; font-src 'self'; img-src 'self' data:; connect-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'self'; form-action 'self';`
   - `unsafe-eval` and wildcard origins (`*`) are strictly prohibited.
   - All runtime assets are local; zero third-party CDN or analytics SaaS scripts.
4. **Anti-Framing & Clickjacking:**
   - `X-Frame-Options: DENY`
   - `frame-ancestors 'none'` in CSP.
5. **Cross-Origin & MIME Isolation:**
   - `X-Content-Type-Options: nosniff`
   - `Referrer-Policy: strict-origin-when-cross-origin`
   - `Cross-Origin-Opener-Policy: same-origin`
   - `Cross-Origin-Resource-Policy: same-origin`
6. **Session & Cookie Security:**
   - Single-use One-Time Code (OTC) exchange via `POST /api/v1/auth/login`.
   - Opaque server-side session IDs (`marshal_session`).
   - `HttpOnly`, `SameSite=Lax`, and `Secure` (on HTTPS/non-loopback).
   - Session fixation protection (ID rotation on authentication).
   - 30-minute idle timeout and 24-hour absolute session ceiling.
7. **CSRF & Origin Hardening:**
   - Per-session HMAC-SHA256 CSRF token validated via `X-CSRF-Token` header on all mutations.
   - `Origin` and `Host` validation against approved loopback and host bindings.
