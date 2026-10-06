# OpenCode and model-provider qualification

MARSHAL implements an OpenCode CLI adapter. OpenCode may use a local
Ollama service or a configured remote model provider, but adapter availability
and canonical Runtime execution are separate claims.

## Prerequisites and probes

```bash
command -v opencode
opencode --version
opencode models

command -v ollama
ollama --version
ollama list
curl -fsS http://127.0.0.1:11434/api/version
```

`marshal adapter probe opencode` verifies the installed OpenCode process
contract. Community Resource Awareness can inventory a loopback Ollama service
and its models. Neither check proves that a model can use tools correctly.

## v1.0.1 qualification results

The release qualification used OpenCode 1.18.16 and required the model to
create a proof file with the requested content. A zero process exit without
the requested file or content counted as `FAIL`.

| Model path | Class | Adapter/model E2E |
|---|---|---|
| `deepseek/deepseek-v4-flash` | Authenticated remote | PASS |
| `deepseek/deepseek-v4-pro` | Authenticated remote | PASS |
| `ollama/qwythos-9b` | Local | FAIL — proof content did not match the task |
| `ollama/blackarch-ai` | Local | FAIL — proof content did not match the task |
| `ollama/huihui_ai/qwen2.5-coder-abliterate:14b` | Local | FAIL — proof file was not created |

These are direct adapter results. Canonical Runtime, MCP, and A2A provider E2E
are `NOT_RUN` in v1.0.1 because provider traffic needs an endpoint-enforcing
network backend.

## Runtime security boundary

Governed OpenCode runs keep `--unshare-net` and use a per-run Unix proxy bridged
by trusted socat. The model provider determines the single default API endpoint;
`ollama/…` defaults to `127.0.0.1:11434`. An alternative `OLLAMA_HOST` needs an
exact operator grant via `/egress`. Unsupported provider prefixes or unavailable
isolation/bridge fail closed. Native sessions opened directly are out of scope.
See [network egress enforcement](../network-egress-firewall.md).

## Model compatibility

Conversational generation does not imply structured tool use. Before relying
on any OpenCode model, require a task-level test that verifies the requested
filesystem change and content. Do not count `opencode models`, a successful
version probe, or exit status zero as E2E verification.
