# Web Control Plane architecture

The v1.0.1 Web UI is a React/TypeScript application compiled into hashed
assets and embedded in the Go binary. `marshal web serve` binds to
`127.0.0.1:8787` by default and requires an initialized canonical runtime.

```text
operator browser
      |
loopback HTTP + one-time login + session + CSRF
      |
internal/webcontrol
      |
allowlisted live handlers
      |
app.Runtime / MemoryService / SQLite / Resource collector
```

The production CLI path never falls back to nil-runtime demo state. A central
live-runtime boundary returns `501 Not Implemented` for registered routes that
still have only an in-memory fixture implementation.

Security properties include HttpOnly session cookies, bounded single-use login
codes, mutation CSRF checks, CSP, anti-framing and MIME headers, correlation
IDs, loopback binding by default, and route authority checks. The browser never
opens SQLite or receives raw environment variables/provider secrets.

See [API reality](API-REALITY-MATRIX.md) for the exact live surface.
