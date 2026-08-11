# Protocol Negotiation

## Order

```text
discover endpoint/card
→ identify protocol
→ identify advertised version
→ compare supported version
→ negotiate extensions
→ authenticate/authorize
→ create session/task/tool call
```

## A2A

Agent OS supports A2A `1.0`.

A version mismatch returns an explicit incompatibility; do not silently interpret
a different major/minor protocol.

## MCP

Agent OS pins exact MCP specification date for the runtime profile.

Unknown/newer protocol:
```text
probe_required
```

rather than guessing compatibility.

## CLI

```bash
python tools/protocol_check.py a2a 1.0 1.0
python tools/protocol_check.py mcp 2026-07-28 2026-07-28
```
