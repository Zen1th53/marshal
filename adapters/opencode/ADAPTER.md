# OpenCode Adapter

## Native Surfaces

- Project rules: `AGENTS.md`.
- Custom instruction files via `opencode.json`.
- Built-in agent/subagent model with per-agent permissions.
- Headless HTTP server via `opencode serve`.
- OpenAPI surface and event stream.
- MCP client support.
- Session/export/server APIs.

## Recommended Bootstrap

Use `AGENTS.template.md`.

Do not configure the entire `agents/` tree as unconditional remote/local
instructions. This would bypass Agent OS progressive context loading.

## Runtime Integration

OpenCode's server/OpenAPI surface is a strong adapter target:

```text
Agent OS Runtime
↔ OpenCode server
↔ OpenCode agent/session
```

Bind work to project/cwd, task, and Agent OS identity.

## Permissions

Map OpenCode per-agent permissions to Agent OS semantic capabilities.

More permissive native config does not override Agent OS deny/approval rules.

## Remote Instruction Warning

OpenCode can load remote instruction URLs. Agent OS treats remote instructions as
external/untrusted content unless explicitly promoted by trusted project policy.
