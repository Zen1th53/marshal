# Getting Started

SLAVES supports two complementary modes:

- **File-first mode** uses the role, protocol, memory, and template files
  directly. It requires no daemon.
- **Local runtime mode** uses the Go `0.1.0` executable for transactional task
  state, policy, worktrees, workers, events, and artifacts.

## Prerequisites

- Git and a Git repository checkout
- Python 3 for pack and conformance tooling
- Go 1.25 or newer for the local runtime
- Linux for Runtime V1 worker execution
- Codex CLI for Codex-backed task runs
- bubblewrap (`bwrap`) for strong sandboxing

Codex and bubblewrap are optional for reading the pack or running static
validation. `slaves doctor` reports their actual availability.

## Validate the pack

```bash
python conformance/runner.py validate-pack
python -m unittest discover -s conformance/tests -v
```

Start an agent with [AGENT-BOOTSTRAP.md](../AGENT-BOOTSTRAP.md). Select a
vendor integration using [bootstrap/ADAPTER-SELECTION.md](../bootstrap/ADAPTER-SELECTION.md)
and verify capabilities against [adapters/MATRIX.json](../adapters/MATRIX.json).

## Install and initialize the local runtime

From the repository root:

```bash
go install ./cmd/slaves
slaves init
slaves doctor
```

`slaves init` is idempotent. It creates private runtime state under the ignored
`.slaves/` directory; it does not invent tasks or overwrite governance files.

Start the daemon in one terminal:

```bash
slaves daemon
```

Register a developer agent in another terminal:

```bash
slaves status
slaves agent register --name local-codex --role developer
slaves agents
```

Import one task or an array of tasks:

```json
{
  "id": "TASK-001",
  "title": "Implement the scoped change",
  "status": "ready",
  "risk": "R1",
  "revision": 0
}
```

```bash
slaves task import tasks.json --dry-run
slaves task import tasks.json
slaves tasks
slaves task show TASK-001
```

Run with the agent ID returned by registration:

```bash
slaves run TASK-001 --adapter codex --agent AGENT-ID
slaves events
slaves artifacts
slaves verify
```

The runtime transitions successful implementation work to review; it does not
grant Developer self-approval for QA or AppSec.

## Continue reading

- [Concepts](concepts.md)
- [Architecture](architecture.md)
- [Runtime](runtime.md)
- [Adapters](adapters.md)
- [Conformance](conformance.md)
- [Security model](security-model.md)
