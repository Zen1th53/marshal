# MARSHAL examples

The examples in this directory use public, synthetic data and are intended to
be run from the repository root.

## Policy conformance

`policy-test-suite.json` is a complete declarative policy test. It embeds a
deny-by-default policy, binds the test to that policy's canonical SHA-256
digest, and expects the denial. It does not contact a provider or execute the
resource named in the fixture.

```bash
marshal policy test examples/policy-test-suite.json
```

The expected result is `PASS`. A passing test is evidence about policy behavior;
it is not runtime authorization.

Provider-backed task examples are documented in
[Getting started](../docs/getting-started.md). They require the selected
provider binary and the operator's own authentication.

## Provider task inputs

- `01-codex-task.json` is a valid task import for the Codex adapter.
- `02-opencode-ollama-task.json` is a valid task import for OpenCode with a
  separately selected Ollama model.

Import these files only in a disposable or intentionally selected repository:

```bash
marshal task import examples/01-codex-task.json --dry-run
marshal task import examples/02-opencode-ollama-task.json --dry-run
```

`03-mcp-request.json` is a credential-free JSON-RPC request body suitable for
the authenticated curl flow in the [MCP guide](../docs/mcp.md).
`04-a2a-agent-card.json` is a bounded response example. The live A2A card
reports the release binary's build-aware version instead of the `dev` value
shown for source builds. Never put plaintext bearer tokens in these files.
