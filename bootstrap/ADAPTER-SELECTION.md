# Adapter Selection

## Rule

Prefer the agent the user explicitly chose.

If no agent is chosen, select by required native capability, not hype.

## Examples

Need:
- rich headless structured output + MCP + policy → choose an adapter that probes native support.
- implementation worker only → Aider may be sufficient.
- server/OpenAPI integration → OpenCode is a strong native target.
- CLI-native instruction hierarchy → Gemini/Codex/Claude/OpenCode/Crush can fit through their native context files.

## Probe First

Use:

```bash
python conformance/runner.py probe-adapters
```

Then consult `adapters/MATRIX.json`.

Installed-version evidence outranks the dated compatibility snapshot.
