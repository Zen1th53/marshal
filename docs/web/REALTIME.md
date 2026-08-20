# MARSHAL Web Control Plane — Realtime Event Streaming Specification

**Protocol:** Server-Sent Events (`text/event-stream; charset=utf-8`)  
**Task:** T177  

This document specifies the unidirectional realtime event streaming architecture from the MARSHAL runtime to operator browsers.

---

## 1. Protocol & Connection Lifecycle

- **Endpoint:** `GET /api/v1/events/stream`
- **Replay & Backfill:** Supports standard `Last-Event-ID` header and `?last_event_id=N` parameter.
- **Ordered IDs:** Every event has a strictly increasing integer sequence `id: N`.
- **Replay Window:** Bounded in-memory ring buffer of the latest 100 events (`MaxReplayBufferSize`).
- **Resync Protocol:** If the client requests an ID that has already been purged from the replay buffer, the server emits a `resync` event:
  ```text
  id: 151
  event: resync
  data: {"reason":"replay_window_expired"}
  ```
  The browser client responds by performing a full state refetch.
- **Heartbeat:** To detect network drops and keep long-lived connections open through stateful proxies, the server sends a comment ping every 15 seconds:
  ```text
  : heartbeat 1755678900
  ```
- **Slow Client Protection:** Per-client non-blocking channel with a bounded buffer (64 events). Saturated slow clients are disconnected.
- **Session Scoping & Redaction:** Events are filtered based on the client session's role and capability scope. High-entropy keys, passwords, and private secrets are never serialized into event payloads.
