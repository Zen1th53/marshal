# Adapter Compatibility Matrix

`adapters/MATRIX.json` is the machine-readable source.

Status meanings:

- **native** — upstream exposes a direct surface.
- **emulated** — Agent OS wrapper/runtime must provide the behavior.
- **probe_required** — known/possible native surface, but installed version must be checked.
- **unsupported** — do not pretend native support exists.

The matrix is a dated compatibility snapshot, not permanent truth.

## Strongest Native Integration Targets

- Gemini CLI: structured headless + MCP + policy + context hierarchy.
- Codex: AGENTS + non-interactive + sandbox/approval + server surfaces.
- Claude Code: CLAUDE + structured print mode + MCP + permissions/hooks.
- OpenCode: AGENTS + OpenAPI headless server + per-agent permissions + MCP.
- Crush: rich session/MCP/hooks/permissions architecture, but runtime surface should be probed.
- Aider: strong implementation worker; orchestration/policy/memory mostly belongs in the wrapper.
