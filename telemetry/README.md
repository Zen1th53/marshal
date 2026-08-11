# Telemetry Plane

Agent OS telemetry follows OpenTelemetry concepts while keeping a stable custom
`agentos.*` namespace for Agent OS-specific attributes.

This avoids falsely treating development-stage GenAI semantic conventions as a
permanent Agent OS wire contract.

Goals:
- correlate task/session/agent/artifact activity,
- measure latency/retries/cost where available,
- detect policy denials and verification invalidation,
- never log hidden reasoning or secrets.
