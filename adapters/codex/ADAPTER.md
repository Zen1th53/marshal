# Codex CLI Adapter

## Native Surfaces

- Project instructions: `AGENTS.md`.
- Non-interactive automation: `codex exec`.
- Sandbox/approval settings are explicit in non-interactive mode.
- Codex exposes programmatic server surfaces including App Server and MCP Server.
- Session/resume and configuration are native Codex concepts.

## Recommended Bootstrap

Use `AGENTS.template.md` as a small root adapter.

The root file points to `agents/AGENT-BOOTSTRAP.md`; it must not duplicate the
full doctrine/memory pack.

## Headless

For runtime automation use the native non-interactive surface and capture
structured/event output when supported by the installed version.

Always bind evidence to:
- Codex version,
- session/thread ID when available,
- repository HEAD,
- sandbox/approval mode.

## MCP / App Server

Prefer stable documented surfaces for the installed version.

Do not assume experimental or fast-moving event fields without probing.

## Security

Agent OS policy can only narrow native Codex permissions unless an explicit owner
policy says otherwise.

Do not use broader sandbox modes merely to avoid an approval integration problem.
