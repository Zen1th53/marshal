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
- Current execution mode (e.g. Standard or ULTRA)
- Timestamp of app start and background heartbeat intervals

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
