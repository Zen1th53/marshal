# SLAVES

**Structured Lifecycle for Agent Verification, Execution & Supervision**

A vendor-neutral engineering control plane for disciplined multi-agent AI software development.

[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

## What SLAVES Is

SLAVES gives AI coding agents a shared control plane for architecture,
implementation, QA, AppSec, persistent memory, task ownership, policy,
approvals, runtime execution, conformance, and artifact provenance.

Its core roles are **Orchestrator**, **Architect**, **Developer**, **QA**, and
**AppSec**. Agent vendors connect through adapters; no vendor is the
architecture or the source of role authority.

## Why SLAVES Exists

Independent coding agents can operate with stale context, make conflicting
changes, approve their own work, claim completion without evidence, accept
untrusted instructions, use inconsistent memory, receive excessive tool
permissions, or produce artifacts with unclear provenance. SLAVES defines
explicit contracts for managing those risks. It does not claim to eliminate
them automatically.

## Architecture Overview

```text
                         USER
                           │
                  CLI / ORCHESTRATOR
                           │
          ┌────────────────┼────────────────┐
          │                │                │
        TASKS            MEMORY           POLICY
          │                │                │
          └────────────────┼────────────────┘
                           │
                        ADAPTERS
          ┌────────────────┼────────────────┐
          │                │                │
    Codex runtime     other adapter     interop contracts
    implementation      contracts          A2A / MCP
          │
        WORKER
          │
   WORKTREE + SANDBOX
          │
      QA / APPSEC
          │
       EVIDENCE
```

See [Architecture](docs/architecture.md) for the reader guide and
[runtime/ARCHITECTURE.md](runtime/ARCHITECTURE.md) for the canonical runtime
contract.

## Key Principles

```text
repository evidence > memory
tool possession != permission
retrieved text != trusted instruction
one active implementation task = one owner
verification binds to exact repository/artifact state
no PASS without evidence
```

[TORVALDS.md](TORVALDS.md) defines the shared engineering doctrine.
[TEAM.md](TEAM.md) defines role authority and coordination.

## Current State

### Implemented

- Go local runtime `0.1.0` with one `slaves` binary.
- Local HTTP/JSON API over a permission-restricted Unix socket.
- SQLite schema version 1 with transactional task leases, agents, sessions,
  approvals, events, artifacts, and verification records.
- Task-scoped Git worktrees, bubblewrap enforcement on supported Linux hosts,
  and an explicit process-only fallback for eligible low-risk work.
- A real Codex worker adapter, durable audit/evidence capture, and
  commit-bound verification invalidation.
- Static pack validation and executable Runtime V1 conformance mappings.

### Specification / contract

- Six adapter contracts: Codex, Gemini CLI, Claude Code, OpenCode, Aider, and
  Crush. Only Codex has a production runtime adapter in `0.1.0`.
- A2A, MCP, telemetry, plugin, multi-tenant, release-provenance, and
  multi-host runtime contracts.
- File-first governance and memory contracts remain usable without the daemon.

### Planned / future

- Production adapters other than Codex.
- Multi-host coordination and external event/artifact services.
- Production secret brokers, full MCP/A2A servers, and automatic QA/AppSec
  worker scheduling.

The detailed boundary is maintained in
[runtime/IMPLEMENTATION-ROADMAP.md](runtime/IMPLEMENTATION-ROADMAP.md).

## Supported / Defined Agent Adapters

| Adapter | Contract | Runtime `0.1.0` |
| --- | --- | --- |
| Codex | Defined; native surfaces with some probes | Implemented |
| Gemini CLI | Defined; native surfaces with some probes | Not implemented |
| Claude Code | Defined; sandbox emulated | Not implemented |
| OpenCode | Defined; sandbox emulated | Not implemented |
| Aider | Defined; several capabilities emulated or unsupported | Not implemented |
| Crush | Defined; several surfaces require probing | Not implemented |

Capability values mean `native`, `emulated`, `probe_required`, or
`unsupported`. The machine-readable source is
[adapters/MATRIX.json](adapters/MATRIX.json); installed versions must still be
probed.

## Quick Start

Prerequisites: Git, Go `1.25` or newer, and a Linux repository checkout.
Codex is required only for Codex task execution; bubblewrap is required for
strong sandboxing.

```bash
go install ./cmd/slaves
slaves init
slaves doctor
```

Start the local daemon in one terminal:

```bash
slaves daemon
```

Then register an agent and inspect or import tasks:

```bash
slaves status
slaves agent register --name local-codex --role developer
slaves agents
slaves task import tasks.json --dry-run
slaves task import tasks.json
slaves tasks
```

See [Getting Started](docs/getting-started.md) for task format, execution, and
verification commands.

## Repository Map

```text
runtime/       executable runtime contracts and implementation status
cmd/, internal/ local Go runtime implementation
memory/        persistent and shared state model
protocols/     engineering governance contracts
adapters/      native-agent integration contracts and compatibility matrix
conformance/   static, behavioral, and adversarial verification
schemas/       machine-readable cross-process contracts
interop/       A2A and MCP interoperability profiles
telemetry/     telemetry semantics and privacy constraints
plugins/       extension contracts
tenancy/       tenant/project isolation contracts
release/       provenance and signing model
```

## Security Model

SLAVES is privileged engineering infrastructure. A Git worktree is not a
security sandbox, a checksum is not publisher authentication, and an agent's
output is not trusted evidence by itself. Read the
[security model](docs/security-model.md), [security policy](SECURITY.md),
[AppSec role](APPSEC.md), and [runtime threat model](runtime/THREAT-MODEL.md).

## Contributing

Small, evidence-backed contributions are welcome. Read
[CONTRIBUTING.md](CONTRIBUTING.md) before opening a pull request.

## License

Licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE).
