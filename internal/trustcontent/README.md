# Trust-content firewall

`trustcontent` is MARSHAL's provider-neutral prompt-injection boundary. It
assigns every segment a closed trust zone from its transport/source, not from
the segment text. The order is `system`, `owner_policy`, `project_policy`,
`trusted_tool`, `repository_data`, `web_data`, then `untrusted_content`.

Use `Engine.Ingest` for stateful source ingestion. It requires a repository,
byte sanitizer, and authority callback; absent authority fails closed. It
stores only the immutable segment ID, source ID, zone, SHA-256 digest/content
reference, state, idempotency key, and UTC creation time in
`trusted_content_segments`. Raw segment bodies are never persisted.

Use `Engine.Render` or `Renderer.Render` to produce provider context. The
renderer emits ordered `<marshal-trust-zone zone=...>` delimiters and JSON
encoded bodies, so source text cannot create or close a delimiter. Provider
adapters receive the result through `adapter.Request.TrustedContext`.

Success emits `trustcontent.segment.ingested`,
`trustcontent.zone.assigned`, and, for an authenticated render,
`trustcontent.rendered`. Events contain IDs, zones, and digests only. The
optional `trustcontent.injection.suspected` event remains available for a
bounded detector but detection is not an authority mechanism.

Failures use stable codes: `TRUST_ZONE_INVALID`,
`TRUST_UPGRADE_FORBIDDEN`, `TRUST_SEGMENT_TOO_LARGE`, and
`TRUST_RENDER_FAILED`. The engine exposes bounded `trust_content` metrics for
successes, denials, invalid input, errors, duration, and the last safe reason.

Retries converge by immutable ID/idempotency key; a restart reloads the
durable record and resumes only the legal `ingested → zoned → rendered`
transitions.
The firewall does not claim to detect every injection or secret. Its security
property is structural provenance separation, with the configured sanitizer
rejecting known secret material before persistence or rendering.
