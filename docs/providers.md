# Provider adapters

MARSHAL v1.0.1 implements four runtime adapters. Provider availability and
release verification are separate facts.

| Adapter path | Binary | Implemented | Adapter/model E2E | Canonical Runtime E2E |
|---|---|---:|---|---|
| Codex | `codex` | Yes | NOT_RUN | NOT_RUN |
| OpenCode + DeepSeek V4 | `opencode` | Yes | PASS — Flash and Pro | NOT_RUN — enforcing egress unavailable |
| OpenCode + Ollama | `opencode` | Yes | FAIL — tested local models did not complete the strict proof task | NOT_RUN — enforcing egress unavailable |
| Gemini CLI | `gemini` | Yes | NOT_RUN | NOT_RUN |
| Claude Code | `claude` | Yes | NOT_RUN | NOT_RUN |

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

In v1.0.1, a provider that needs network access is rejected with
`NET_ENFORCEMENT_UNAVAILABLE`: the available proxy is not an enforcing
Bubblewrap network backend, so opening the namespace would broaden egress.
The OpenCode results above are direct adapter qualification, outside the
canonical Runtime/MCP/A2A chain; they are not represented as Runtime E2E.

Adapters do not weaken MARSHAL policy or sandbox requirements. Missing
binaries, credentials, required flags, Bubblewrap, or enforceable egress cause
the run to fail or degrade according to the documented boundary; they are not
converted into a false PASS.

The compatibility contracts in [`adapters/MATRIX.json`](../adapters/MATRIX.json)
also describe tools such as Aider and Crush. Those entries are interoperability
contracts, not v1.0.1 runtime adapters.

See [OpenCode and Ollama](providers/opencode-ollama.md) for local setup notes.
