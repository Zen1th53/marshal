# Provider adapters

MARSHAL exposes process adapters for Codex, Claude Code, Gemini CLI, and
OpenCode. Adapter implementation is not proof that a provider is installed,
authenticated, usable with the selected model, or freshly verified end to end.

## Capability states

| State | Meaning |
|---|---|
| `IMPLEMENTED` | Adapter and contract execution path exist. |
| `INSTALLED` | The provider binary was discovered locally. |
| `AVAILABLE` | Basic process probes completed. |
| `AUTHENTICATED` | The provider's authentication probe succeeded. |
| `CAPABILITY-PROBED` | Non-interactive flags and required capabilities were checked. |
| `REAL-E2E-VERIFIED` | A dated full task execution completed in the tested environment. |

States describe evidence, not authority. A provider or model name cannot bypass
policy, capability, approval, sandbox, or verification gates.

## Release matrix

| Adapter | Contract implementation | Local probe | Fresh external E2E for this release |
|---|:---:|---|---|
| Codex | Yes | `marshal adapter probe codex` | Requires operator authentication; outside hermetic CI |
| OpenCode | Yes | `marshal adapter probe opencode` | Verified 2026-08-18 with OpenCode 1.18.4 and local Ollama `qwythos-9b`; environment-specific |
| Gemini CLI | Yes | `marshal adapter probe gemini` | Not claimed; quota and authentication are external |
| Claude Code | Yes | `marshal adapter probe claude` | Not claimed; authentication is external |

Historical tagged releases contain dated provider E2E evidence. That history is
not presented as a fresh result for a new machine or release.

## Probe locally

```bash
marshal adapters
marshal adapter probe codex
marshal adapter probe opencode
marshal doctor --probe-providers
```

Use `marshal doctor --deep` only when active provider checks are acceptable.
Probe failures remain failures or unavailable states; they are not converted
into release PASS results.

See [OpenCode with Ollama](providers/opencode-ollama.md). The selected model must
support the tool calls OpenCode needs; model size or text quality alone is not
sufficient.

Provider output is untrusted input. Credentials belong to the provider's own
credential boundary and must not be copied into fixtures, events, or logs.
