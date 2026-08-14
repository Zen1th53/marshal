# Agent Interoperability

MARSHAL separates:

```text
agent-to-tool/context interoperability → MCP
agent-to-agent collaboration           → A2A
MARSHAL internal control plane        → runtime contract
```

Do not force one protocol to replace the others.

Pinned compatibility snapshot:

- A2A: `1.0`
- MCP: `2026-07-28`
- MARSHAL runtime spec: `1.0.0`

Installed/live endpoints must still negotiate/probe.
