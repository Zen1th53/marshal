# Claude Code Adapter

## Native Surfaces

- Project memory/instructions: `CLAUDE.md`.
- Non-interactive: `claude -p`.
- Structured outputs: JSON / stream-json.
- Session continuation/resume: native CLI.
- MCP client support and `claude mcp serve`.
- Native tool allow/deny and permission modes.
- Hooks/managed settings can enforce organization policy.

## Recommended Bootstrap

Keep `CLAUDE.md` short. Point it at `agents/AGENT-BOOTSTRAP.md`.

Avoid recursively importing the whole pack because imports become persistent
context and defeat progressive loading.

## Non-interactive Permissions

Use native permission controls for the smallest tool surface.

For automated permission decisions, the MARSHAL runtime can be exposed through
an MCP permission tool if the installed Claude Code mode supports it.

## Managed Policy

Enterprise/managed settings outrank project/user settings upstream. The adapter
must not attempt to weaken organization policy.

## MCP Trust

Third-party MCP output is external data. Apply instruction-trust and data-policy
rules before acting on retrieved content.
