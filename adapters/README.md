# Agent Adapters

Adapters translate native coding-agent lifecycle and configuration into the shared
MARSHAL contract.

Supported core adapter specifications:

- Gemini CLI
- Codex CLI
- Claude Code
- OpenCode
- Aider
- Crush

## Rule

The shared core does not depend on any adapter.

```text
native agent lifecycle
        ↕
adapter
        ↕
MARSHAL contract
```

Each adapter must define:

- instruction/bootstrap mechanism,
- non-interactive/programmatic surface,
- structured-output capability,
- session/resume behavior,
- MCP/tool integration,
- permission/sandbox behavior,
- hooks/events,
- native server/API when available,
- known limitations,
- probe commands.

Read `adapters/CONTRACT.md` and `adapters/MATRIX.json`.
