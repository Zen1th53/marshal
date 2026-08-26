# Web Control Plane API contract

**Contract version:** `1.0.0`

**Product release:** `v1.0.1`

All JSON endpoints use `application/json; charset=utf-8`. Mutations require an
authenticated session, a session-bound `X-CSRF-Token`, and any route authority
declared by the server.

The production-supported route groups are:

- `GET /api/v1/system/status`;
- `POST /api/v1/auth/login`, session/CSRF inspection, and logout;
- `GET /api/v1/resources`;
- `GET /api/v1/health/doctor`;
- canonical memory search, explain, record/detail, working-memory, and
  mutation routes described in [API reality](API-REALITY-MATRIX.md); and
- backup list/create/verify operations.

Other registered routes retain DTOs and explicit test/demo handlers but return
`501 Not Implemented` when a live runtime is attached. They are not part of
the v1.0.1 production API claim.

Identifiers are opaque URL-safe strings. Timestamps are RFC 3339 UTC. Error
responses use an `error` object containing `code`, `message`, and an optional
`correlation_id`.
