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

Normal telemetry uses registration and event requests over HTTPS. Disabling it
sends one bounded opt-out event when the endpoint is enabled; its fields are
listed below too.

**Registration** (once per install), over HTTPS:

```json
{
  "installation_id": "inst-xxxxxxxxxxxxxxxx",
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
over HTTPS, a batched array:

```json
[
  {
    "kind": "app_start",
    "installation_id": "inst-xxxxxxxxxxxxxxxx",
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
| `kind` | yes | `registration`, `app_start`, `heartbeat`, `mode_change`, `activation_success`, `activation_failure`, `renewal_success`, `renewal_failure`, `error_class`, `telemetry_opt_out` |
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

`/status` and the Board help footer say whether anonymous telemetry is on or off. To check the CLI configuration directly:

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

Marshal turns telemetry off for this installation. The command does not send project content.
When cloud telemetry is enabled, disabling also sends one opt-out event with only
`kind`, `installation_id`, `client_version`, `os`, and `arch`. Re-enabling creates
a new anonymous installation identity and keypair.

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
