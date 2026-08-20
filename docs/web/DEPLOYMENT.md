# MARSHAL Web Control Plane — Production Build & Secure Deployment (T218)

This guide documents the production compilation pipeline, standalone Go embedded asset serving, and supported secure deployment modes.

---

## 1. Zero-Node Production Architecture

The MARSHAL Web Control Plane embeds all production client assets (`HTML`, `CSS`, `JS`) directly into the Go binary at compile time via `embed.FS`.

- **No Runtime Node.js / npm Dependency**: The running MARSHAL daemon requires only the compiled Go binary.
- **Zero External CDN Dependencies**: All fonts, style rules, and scripts are self-contained locally, enabling air-gapped deployment.
- **Reproducible Build Script**:
  ```bash
  ./scripts/build-web.sh
  ```

---

## 2. Supported Deployment Modes

### Mode A: Loopback Local Operator Mode (Default)
- **Interface**: `127.0.0.1:8787` (or `::1`)
- **Transport**: Cleartext HTTP over loopback interface only.
- **Security Boundary**: OS kernel loopback socket isolation.
- **Authentication**: One-time ephemeral operator login token.

### Mode B: Reverse-Proxy TLS Mode (Non-Loopback)
- **Interface**: Reverse proxy (e.g., NGINX / Caddy / Envoy) terminates TLS.
- **Backend Bind**: Loopback socket (`127.0.0.1:8787`).
- **Security Invariant**: Binding directly to `0.0.0.0` or public interfaces without TLS/AllowInsecure flag is blocked by server startup validation (`ErrNonLoopbackInsecure`).

---

## 3. SPA & API Routing Invariant

- **Hashed Static Assets (`/assets/*`)**: Served with `Cache-Control: public, max-age=31536000, immutable`.
- **Client Application Shell (`/`, `/tasks`, `/memory`, `/operations`)**: Served with `Cache-Control: no-cache, no-store, must-revalidate` and SPA fallback to `index.html`.
- **API Endpoints (`/api/*`)**: **Never** intercepted by SPA fallback; nonexistent API endpoints strictly return standard JSON 404 error envelopes (`{"error":{"code":"api_endpoint_not_found"}}`).
