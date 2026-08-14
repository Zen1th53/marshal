# Gemini CLI Adapter

## Native Surfaces

- Project context: `GEMINI.md`.
- Context filename can be configured to include `AGENTS.md`.
- Headless: `gemini -p`.
- Structured output: JSON and streaming JSON.
- MCP: native client support.
- Policy/tool confirmation: native policy engine.
- ACP mode: native protocol surface.
- Sandboxing/trusted-folder features exist upstream.

## Recommended Bootstrap

Place a short project `GEMINI.md` based on `GEMINI.template.md`.

Do not import the entire pack into GEMINI context. Let it instruct Gemini to read
`agents/AGENT-BOOTSTRAP.md` and then conditionally load files.

## Programmatic Mode

Prefer structured headless output for runtime orchestration.

Probe the installed CLI before relying on exact flags or experimental features.

## MCP

Expose the MARSHAL runtime/memory service through MCP when implemented.

Server trust must not bypass MARSHAL capability policy.

## Native Policy

Map native tool policy to `CAPABILITIES.yaml`.

MARSHAL deny remains deny even if Gemini native config would allow it.

## Known Caution

Plan/subagent features may change maturity. Conformance tests should probe the
installed version instead of freezing assumptions.
