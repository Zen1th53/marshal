# Web realtime events

`internal/webcontrol` contains a bounded Server-Sent Events hub with ordered
IDs, replay, heartbeat, and slow-client protection. In v1.0.1 the hub is used
by explicit tests/demo servers, but the canonical runtime does not yet publish
its durable event stream into that hub.

Consequently, `GET /api/v1/events/stream` returns `501 Not Implemented` when
`marshal web serve` attaches a live runtime. Runtime-to-Web event publication
is a follow-up item and is not a v1.0.1 production claim.
