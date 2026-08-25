# Getting started with MARSHAL

This guide covers the first local Community workflow. Install MARSHAL using
the [installation guide](installation.md), then enter an existing Git
repository.

## Initialize and diagnose

```bash
cd /path/to/repository
marshal init
marshal version
marshal doctor
```

`marshal init` creates missing `CAPABILITIES.yaml`, `PACK-VERSION.yaml`, and
`RUNTIME-VERSION.yaml` defaults and initializes `.marshal/state.db`. Existing
regular defaults are preserved. The current release reports v1.0.1 and schema
v72.

Default `doctor` does not execute optional provider probes; those rows are
reported as `NOT_RUN`. Probe installed provider CLIs explicitly:

```bash
marshal doctor --probe-providers
marshal adapters
```

## Start the daemon

Run the daemon in one terminal:

```bash
marshal daemon
```

Use another terminal in the same repository:

```bash
marshal status
```

The daemon listens on `.marshal/runtime.sock` with mode `0600`.

## Register an agent and import a task

```bash
marshal agent register --name local-developer --role developer

cat > tasks.json <<'JSON'
[
  {
    "id": "TASK-001",
    "title": "Add a repository status check",
    "status": "ready",
    "risk": "R1"
  }
]
JSON

marshal task import tasks.json --dry-run
marshal task import tasks.json
marshal tasks
marshal task show TASK-001
```

## Optional provider execution

After the selected provider passes its local probe:

```bash
marshal run TASK-001 --adapter codex
```

The implemented adapter names are `codex`, `opencode`, `gemini`, and `claude`.
Execution may fail closed if policy, credentials, Bubblewrap, or enforceable
network isolation is unavailable. A binary probe is not an authenticated E2E
verification.

## Backup and Web UI

With the daemon running:

```bash
marshal state backup --output ./marshal-backup.db
marshal state verify-backup ./marshal-backup.db
marshal web serve
```

The Web command binds to loopback by default and prints a single-use login
URL. A live Web server never falls back to demo data; fixture-only panels
return `501 Not Implemented`.

Continue with the [CLI reference](cli.md), [provider guide](providers.md), and
[security model](security-model.md).
