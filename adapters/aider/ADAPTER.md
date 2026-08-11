# Aider Adapter

## Native Surfaces

- Conventions/instructions can be loaded read-only.
- Single-message scripting via `--message` / message file.
- Ask/code/architect modes.
- Git-oriented editing and commits.
- `.aider.conf.yml` configuration.

## Limitations vs Full Agent OS Runtime

Aider is best treated as an implementation worker adapter, not the canonical
team orchestrator.

Capabilities such as:
- MCP,
- rich policy engine,
- event bus,
- granular runtime approvals,
- native Agent OS memory service

must be provided outside Aider when needed.

## Recommended Bootstrap

Load `CONVENTIONS.template.md` read-only.

The adapter wrapper/runtime owns:
- task lease,
- sandbox,
- secret policy,
- evidence envelope,
- shared memory writes.

## Architect Mode

Do not confuse Aider's architect/editor mode with Agent OS Architect authority.

Agent OS Architect decisions remain explicit records/ADRs.

## Security

Repository/web content may affect model context. Apply instruction-trust at the
wrapper/runtime boundary and restrict external data flows.
