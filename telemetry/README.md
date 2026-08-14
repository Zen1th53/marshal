# Telemetry Plane

MARSHAL telemetry follows OpenTelemetry concepts while keeping a stable custom
`marshal.*` namespace for MARSHAL-specific attributes.

This avoids falsely treating development-stage GenAI semantic conventions as a
permanent MARSHAL wire contract.

Goals:
- correlate task/session/agent/artifact activity,
- measure latency/retries/cost where available,
- detect policy denials and verification invalidation,
- never log hidden reasoning or secrets.
