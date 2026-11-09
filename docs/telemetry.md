# Telemetry and Privacy

Marshal includes lightweight, anonymous telemetry to help the development team understand version adoption, operating system distribution, and runtime stability.

Telemetry is **anonymous** and **opt-out**. It is sent to the Community Cloud authority at:

```text
https://marshal.blackhat.uz
```

---

## What is Collected

When telemetry is enabled, Marshal sends periodic heartbeat and startup events containing only high-level metadata:

- A randomly generated local installation identifier (e.g. `inst-xxxx`)
- A public signing key generated locally (the private key never leaves your machine)
- The Marshal binary version (e.g. `0.2.0`)
- Operating system and architecture (e.g. `linux/amd64`)
- The run mode Marshal is in
- Timestamp of app start and background heartbeat intervals

---

## Exactly What Is Sent, on the Wire

Two HTTPS requests, JSON bodies. Every field either party sees is below — there
is no other payload.

**Registration** (once per install) — `POST /v1/installations/register`:

```json
{
  "installation_id": "inst-3f9a1c2b7e4d8a60",
  "public_key": "b3zH1Qc9ukE2y0rW...",
  "client_version": "0.2.4",
  "os": "linux",
  "arch": "amd64"
}
```

- `installation_id` — random local ID (`inst-` + 16–64 hex); delete the state
  dir and it is gone.
- `public_key` — base64url ed25519 **public** key made on your machine; the
  private key never leaves it.
- `client_version`, `os`, `arch` — describe the build, not who runs it.

**Events** (app start, then a heartbeat every 2 minutes) —
`POST /v1/telemetry/events`, a batched array:

```json
[
  {
    "kind": "app_start",
    "installation_id": "inst-3f9a1c2b7e4d8a60",
    "client_version": "0.2.4",
    "os": "linux",
    "arch": "amd64",
    "mode": "standard",
    "occurred_at": "2026-11-09T06:23:46Z"
  }
]
```

| Field | Always | Values |
|---|---|---|
| `kind` | yes | `registration`, `app_start`, `heartbeat`, `mode_change`, `activation_success`, `activation_failure`, `renewal_success`, `renewal_failure`, `error_class` |
| `installation_id` | yes | the random ID above |
| `client_version`, `os`, `arch` | yes | e.g. `0.2.4`, `linux`, `amd64` |
| `mode` | when known | `standard` or `ultra` |
| `occurred_at` | yes | RFC 3339 UTC timestamp |
| `session_id` | optional | random per-run ID (`sess-…`) |
| `error_class` | only on `error_class` events | a **coarse category only**: `network`, `auth`, `lease_expired`, `lease_invalid`, `rate_limited`, `server`, `client`, `sandbox`, `unknown` — never a message or detail |

The only extra request header is `MARSHAL-Client-Version`. No cookies, no
account, no telemetry auth token. A failed or timed-out send is dropped
silently and never changes what Marshal does.

---

## What is NEVER Collected

Marshal adheres to strict privacy guarantees. Telemetry **never** collects, transmits, or logs:

- No source code or project file contents
- No user prompts, instructions, or goals
- No agent model outputs or conversation text
- No file names, directory paths, or repository URLs
- No Git commit messages, diffs, or author identities
- No environment variables, API keys, or credentials
- No terminal commands or execution logs

---

## Checking Telemetry Status

To check your current telemetry configuration:

```bash
marshal telemetry status
```

Example output:

```text
Anonymous telemetry: enabled (marshal.blackhat.uz).
```

When using `--json`, the command returns `{ "enabled": true }` or `{ "enabled": false }`.

---

## How to Opt Out

You can disable telemetry at any time using either the CLI or an environment variable.

### Option 1: Via the Marshal CLI

Disable telemetry with a single command:

```bash
marshal telemetry disable
```

Marshal immediately stops all telemetry reporting and transmits an anonymous opt-out signal to the endpoint so the installation ID is unlinked.

To verify:

```bash
marshal telemetry status
# Output: Anonymous telemetry: disabled.
```

### Option 2: Via Environment Variable

You can disable all cloud connectivity and telemetry across all sessions by setting `MARSHAL_CLOUD_ENDPOINT=off` in your shell environment:

```bash
export MARSHAL_CLOUD_ENDPOINT=off
```

You can add this line to your `~/.bashrc`, `~/.zshrc`, or CI environment. When set to `off`, `none`, `disabled`, `0`, or `false`, the cloud client is completely disabled at startup, no network connections are attempted, and Marshal operates entirely offline.

---

## Re-Enabling Telemetry

If you previously opted out and want to re-enable telemetry:

```bash
marshal telemetry enable
```

Re-enabling creates a fresh, random installation identity and keypair.
