# Credential broker

Credential use by governed workers is off by default. Run `/permission
credential request <codex|claude|gemini|opencode>` to open MARSHAL's fixed
English permission prompt. Only uppercase `A` allows; everything else denies. A
worker request can also queue this prompt. After allowing, retry the task. The
decision is remembered for this project. `/permission credential revoke
<provider>` revokes it and closes active broker connections. A message from a
model never grants access.

## Register and evaluate a worker

Register each worker with its provider before running a task:

```sh
marshal agent register --name codex-worker --role developer --provider codex
marshal agent register --name claude-worker --role developer --provider claude
```

Evaluate the worker result from `marshal run`, `marshal logs <task-id>`, and
`marshal task show <task-id>`. A successful CLI exit alone does not establish
success: `marshal run` can exit 0 while reporting a failed worker. Require a
successful worker result with exit status 0 and the expected task output/status.

## Supported credentials

| Worker | Host credential source | Injection host and field |
| --- | --- | --- |
| Codex ChatGPT subscription | `$CODEX_HOME/auth.json` (`~/.codex/auth.json`), managed `chatgpt` sign-in | `chatgpt.com:443`, `Authorization: Bearer` and `Chatgpt-Account-Id` |
| Codex API key | `OPENAI_API_KEY`, or `$CODEX_HOME/auth.json` (`~/.codex/auth.json`) field `OPENAI_API_KEY` | `api.openai.com:443`, `Authorization: Bearer` |
| Claude Code subscription | `$CLAUDE_CONFIG_DIR/.credentials.json` (`~/.claude/.credentials.json`), `claudeAiOauth` | `api.anthropic.com:443`, `Authorization: Bearer` |
| Claude Code API key | `ANTHROPIC_API_KEY` | `api.anthropic.com:443`, `x-api-key` |
| Gemini API key | `GEMINI_API_KEY`, then `GOOGLE_API_KEY` | `generativelanguage.googleapis.com:443`, `x-goog-api-key` or query `key` |
| OpenCode `openai/*` | `OPENAI_API_KEY` | `api.openai.com:443`, `Authorization: Bearer` |
| OpenCode `anthropic/*` | `ANTHROPIC_API_KEY` | `api.anthropic.com:443`, `x-api-key` |
| OpenCode `google/*` | `GOOGLE_GENERATIVE_AI_API_KEY` | `generativelanguage.googleapis.com:443`, `x-goog-api-key` or query `key` |
| OpenCode `deepseek/*` | `DEEPSEEK_API_KEY` | `api.deepseek.com:443`, `Authorization: Bearer` |

Custom provider URLs and credential helpers are not supported. OpenCode's free
and local models keep their existing credential-free behavior.

Codex ChatGPT subscription sign-in works for governed workers through the
broker when the host CLI supports the managed-auth account refresh protocol.
The worker never sees the access token or refresh token. An API key in the host
environment takes precedence over subscription mode.

MARSHAL never writes the host sign-in file. Each injected request reads its
current access token and account ID afresh. Within two minutes of expiry, or
after a provider 401, MARSHAL starts a bounded host `codex app-server --stdio`
process and sends fixed `initialize`, `initialized`, and `account/read
{"refreshToken":true}` messages. The CLI refreshes and persists its own sign-in
under its native coordination. MARSHAL serializes these refreshes across runs,
re-reads the file, and retries a rejected request once. No worker prompt,
placeholder, proxy environment, or sandbox data reaches that host process. Its
diagnostics are discarded. Refresh failure refuses the call; a second 401 is
returned without another refresh.

This protocol was verified in the locally installed Codex 0.160.1 generated
protocol documentation. Older or incompatible CLIs, external auth, keyring-only
sign-in, and incomplete or unrecognized files are unsupported. Host refresh is
limited to 20 seconds and can require the operator to sign in again. No live
provider round trip has been verified.

Claude Code subscription sign-in works for governed workers through the broker.
Each request reads `claudeAiOauth.accessToken` and `expiresAt` (milliseconds)
afresh from the host file. MARSHAL never writes that file or sends its refresh
token anywhere. An `ANTHROPIC_API_KEY` in the host environment takes precedence.

Claude refresh belongs to the native host CLI during normal use, including
MARSHAL's own native Claude session. Within two minutes of expiry, or after a
provider 401, the broker re-reads the host file once. It continues or retries
once only if the access token changed and is fresh. Otherwise it refuses that
request and raises one operator alert per run, including concurrent failures:
"Claude sign-in expired on this machine. Run claude once on the host to refresh
it, then retry." A second 401 also fails closed. There is no polling or refresh
command; the installed Claude 2.1.290 auth help lists no refresh-only command.
Missing or malformed sign-in files refuse work.

## What crosses the sandbox boundary

Each run creates its own CA. The CA private key stays in host memory; real
credentials stay on the host. Only the public certificate, system root bundle,
and random unissued placeholder enter scratch HOME. Codex gets a fresh
`auth.json` with `CODEX_HOME` pointing at that scratch directory. API mode uses
a placeholder `OPENAI_API_KEY`. Subscription mode uses synthetic access,
refresh, ID, and account placeholders, a current `last_refresh`, and an access
expiry 24 hours ahead; its API-key environment value is cleared. The worker CLI
therefore sees a freshly refreshed sign-in. Any sandbox request to
`auth.openai.com`, the token endpoint paths `/oauth/token` or
`/v1/oauth/token`, or a form/JSON top-level `grant_type=refresh_token` is refused and
recorded with a fixed matching rule identifier, without its headers or body.
Inference content mentioning refresh tokens or scratch paths is allowed. The auth host is never used for injection
and requires an ordinary egress grant even for the refusal path. API clients
receive placeholder environment values. Claude subscription mode gets a synthetic
`.credentials.json` under a scratch `CLAUDE_CONFIG_DIR`, with placeholder access
and refresh tokens and `expiresAt` 24 hours ahead; `ANTHROPIC_API_KEY` is cleared.
Claude API mode uses its environment key. Sandbox OAuth/token requests to the
Anthropic API are refused and logged without headers or bodies. The known token
host `platform.claude.com` is refused before dialing, even with an egress grant;
it is never used for TLS termination or credential injection. Native provider
settings, plugins, and auth files are not mounted. The resolved CLI installation
directory is mounted read-only so sibling helper executables are available;
home and credential roots are refused as installation mounts.

The sandbox bridge continues to use the existing per-run Unix proxy. TLS is
terminated only for the selected provider hosts on port 443, after the existing
allowlist and IP checks. The proxy verifies the upstream certificate, binds the
inner request to the CONNECT authority, and swaps exact placeholders only in
the listed fields. Other allowed hosts keep their CONNECT tunnels; destinations
without an egress grant remain refused. Plain HTTP to a broker host is refused.

`SSL_CERT_FILE` points to system roots plus the run CA. `NODE_EXTRA_CA_CERTS`
and Codex's `CODEX_CA_CERTIFICATE` point to the public run CA. Another run's CA
is not included. Public files live in a unique reserved scratch directory;
creation, certificate, root-bundle, or credential failures refuse provider
work. There is no fallback to copying a real credential file.

Each secret field has a distinct stable placeholder. Response redaction maps
access, refresh, ID, and account values back to their corresponding placeholders,
including values from both sides of a host rotation. Account headers therefore
retain the scratch sign-in identity across requests.

`marshal events` and task logs include stored structured egress/broker events,
with refusal reasons and matching rule identifiers.

Responses are buffered and scrubbed with the repository's redaction detector
before returning to the worker. Requests and responses are limited to 32 MiB
with a 30-second TLS/header deadline and a 30-minute exchange deadline.
Incremental streaming, WebSocket upgrades, compressed responses, and
credential-bearing request body fields are refused or unavailable. These limits
can prevent a long provider call. VM tests verified subscription authentication
for Codex and Claude; successful governed file-task completion still needs a VM
retest after the tool-permission fixes.

## Governed worker tool permissions

Governed Codex and Claude workers run non-interactively inside MARSHAL's
bubblewrap sandbox and isolated task worktree. MARSHAL's sandbox, task approval,
and merge review are the security boundary. Codex exec uses
`--dangerously-bypass-approvals-and-sandbox`, the CLI option for an external
sandbox, to disable its nested sandbox and its own approval prompts. Claude
print uses `--permission-mode bypassPermissions --permission-prompts none` so
it can edit and run commands without its own permission prompts. These options
apply only to governed worker adapters. Native sessions retain their own
permission controls.

The broker refuses Codex WebSocket upgrades; Codex falls back to HTTPS.
Synchronous `marshal run` responses can wait for the worker's runtime deadline;
the API still limits response writes and keeps its write timeout on other routes.

## Limits of the guarantee

This protects credential material from governed workers using supported broker
profiles. Subscription requests use a fresh host file read, not a cached token.
Runs longer than the synthetic 24-hour token lifetime must be restarted if the
worker attempts its own refresh. It does not constrain the provider operations
a permitted worker can request, prevent quota spending, or stop it sending
permitted project data to the provider. Provider responses can have
credential-shaped text redacted. The host runtime, upstream provider, system
trust roots, and operator remain trusted. Host compromise and operator-opened
native sessions are outside the governed sandbox boundary. Linux sandbox
enforcement is required today; macOS and WSL enforcement are not verified.