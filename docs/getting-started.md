# Getting started

This guide initializes MARSHAL in a Git repository, imports a task, and runs it
through an installed provider adapter. See [Installation](installation.md) first.

## Initialize and diagnose

```bash
cd /path/to/your/git/repository
marshal init
marshal doctor
marshal adapters
```

`marshal init` creates `.marshal/` and missing project contracts without
overwriting existing contracts. Treat `doctor` output as evidence about this
machine; documentation cannot guarantee provider authentication or sandbox
availability.

## Start the control plane

```bash
marshal daemon
```

In another terminal:

```bash
marshal status
marshal agent register --name operator-agent --role developer
```

Create `tasks.json`:

```json
[
  {
    "id": "TASK-DEMO-001",
    "title": "Add a repository status document",
    "status": "ready",
    "risk": "R1",
    "base_commit": "HEAD",
    "head_commit": "HEAD"
  }
]
```

Validate, import, and inspect it:

```bash
marshal task import tasks.json --dry-run
marshal task import tasks.json
marshal task show TASK-DEMO-001
```

## Execute and inspect

```bash
marshal adapter probe codex
marshal run TASK-DEMO-001 --adapter codex
marshal logs TASK-DEMO-001
marshal events
marshal artifacts
```

For a local OpenCode backend, probe it and select a tool-capable model:

```bash
marshal adapter probe opencode
marshal run TASK-DEMO-001 --adapter opencode --model YOUR_TOOL_CAPABLE_MODEL
```

Provider execution requires the provider's binary, authentication, and quota.
Review the resulting branch and evidence before approving or merging changes.

## Credential-free conformance check

From a MARSHAL checkout:

```bash
marshal policy test examples/policy-test-suite.json
```

This evaluates a policy decision only. It does not execute the resource or
grant authority.

Next: [Provider adapters](providers.md), [Security model](security-model.md), and
[CLI reference](cli.md).
