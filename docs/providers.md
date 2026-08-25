# Provider adapters

MARSHAL v1.0.1 implements four runtime adapters. Provider availability and
release verification are separate facts.

| Adapter | Binary | Implemented | v1.0.1 authenticated E2E |
|---|---|---:|---|
| Codex | `codex` | Yes | NOT_RUN |
| OpenCode + Ollama | `opencode` | Yes | NOT_RUN |
| Gemini CLI | `gemini` | Yes | NOT_RUN |
| Claude Code | `claude` | Yes | NOT_RUN |

The release notes report which binaries were locally probed. A successful
`--version` or help probe does not establish credentials, model compatibility,
remote service access, or successful task execution.

Probe providers without making an E2E claim:

```bash
marshal doctor --probe-providers
marshal adapters
marshal adapter probe codex
marshal adapter probe opencode
marshal adapter probe gemini
marshal adapter probe claude
```

Provider execution is explicit:

```bash
marshal run TASK-001 --adapter codex
marshal run TASK-001 --adapter opencode --model MODEL
```

Adapters do not weaken MARSHAL policy or sandbox requirements. Missing
binaries, credentials, required flags, Bubblewrap, or enforceable egress cause
the run to fail or degrade according to the documented boundary; they are not
converted into a false PASS.

The compatibility contracts in [`adapters/MATRIX.json`](../adapters/MATRIX.json)
also describe tools such as Aider and Crush. Those entries are interoperability
contracts, not v1.0.1 runtime adapters.

See [OpenCode and Ollama](providers/opencode-ollama.md) for local setup notes.
