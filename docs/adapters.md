# Adapters

Adapters map a native coding agent's bootstrap, execution, permission,
session, and evidence surfaces into MARSHAL. They do not change role authority,
policy, or task ownership.

The repository defines contracts for:

- Gemini CLI
- Codex
- Claude Code
- OpenCode
- Aider
- Crush

Runtime `v1.0.0` implements and verifies Codex and OpenCode + local Ollama execution adapters. The other adapter directories contain integration contracts, templates, and probe guidance.

Capability status is explicit:

- `native`: the upstream agent exposes a direct surface;
- `emulated`: MARSHAL must provide the behavior;
- `probe_required`: the installed version must be checked;
- `unsupported`: no native capability is claimed.

Do not copy the compatibility table into another source of truth. Read:

- [adapters/MATRIX.json](../adapters/MATRIX.json)
- [adapters/CONTRACT.md](../adapters/CONTRACT.md)
- [adapters/COMPATIBILITY.md](../adapters/COMPATIBILITY.md)
- [adapter-specific contracts](../adapters/README.md)

Native context files such as `AGENTS.md`, `CLAUDE.md`, `GEMINI.md`, and
`CONVENTIONS.md` stay thin. They point agents to
[AGENT-BOOTSTRAP.md](../AGENT-BOOTSTRAP.md), then progressive context loading
selects only the role and protocols required for the current task.
