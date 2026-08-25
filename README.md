# MARSHAL

[![CI](https://github.com/Zen1th53/marshal/actions/workflows/ci.yml/badge.svg)](https://github.com/Zen1th53/marshal/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/Zen1th53/marshal?color=blue)](https://github.com/Zen1th53/marshal/releases)
[![Go Version](https://img.shields.io/github/go-mod/go-version/Zen1th53/marshal)](https://go.dev)
[![License: AGPL v3](https://img.shields.io/badge/License-AGPL--3.0--only-blue.svg)](LICENSE)

MARSHAL is a local, security-focused runtime and control plane for coding
agents. It coordinates tasks and agents while keeping capability grants,
policy decisions, provider execution, sandboxing, evidence, and durable memory
inside one auditable project runtime.

Raw provider CLIs can edit files and run commands, but they do not provide a
shared source of truth or an independent authorization boundary. MARSHAL adds
those controls around them and fails closed when a requested isolation or
network boundary cannot be enforced.

Current Community release: **v1.0.1**. Current SQLite schema: **v72**.

## Community scope

| Area | Included in v1.0.1 |
|---|---|
| Runtime | Project-local daemon, task/agent leases, Git worktrees, MCP and A2A entry points |
| Policy and security | Capability broker, role authorization, risk gates, secret redaction, deny-by-default network policy |
| Isolation | Linux Bubblewrap execution cells; explicit low-risk process-only opt-in when Bubblewrap is unavailable |
| Providers | Runtime adapters for Codex, OpenCode, Gemini CLI, and Claude Code |
| Evidence | Bounded stdout/stderr artifacts, digests, events, verification results, and provenance links |
| Memory | SQLite-backed recall, task-start context, evidence-linked completion capture, scopes/ACLs, lifecycle governance, conflicts, receipts, and session importers |
| Resource Awareness | Read-only CPU, RAM, storage, accelerator, thermal, cgroup, and local Ollama inventory with conservative advice |
| Operations | Backup verification/recovery, doctor diagnostics, legal evidence export, and an authenticated loopback Web UI |

Community Resource Awareness is advisory. It does not continuously retune
concurrency, context windows, providers, or models. Adaptive resource
governors, aggressive/performance modes, fleet placement, continuous advanced
telemetry, and autonomous organization-wide optimization are not Community
features.

## Install

Release archives are published for Linux amd64 and arm64. Download the archive
for your architecture plus `checksums.txt` from the
[v1.0.1 release](https://github.com/Zen1th53/marshal/releases/tag/v1.0.1), then:

```bash
sha256sum -c checksums.txt --ignore-missing
tar -xzf marshal_1.0.1_linux_amd64.tar.gz
install -Dm755 marshal "$HOME/.local/bin/marshal"
marshal version
```

To build from source:

```bash
git clone https://github.com/Zen1th53/marshal.git
cd marshal
go build -o ./bin/marshal ./cmd/marshal
./bin/marshal version
```

Requirements:

- Linux on amd64 or arm64;
- Git;
- Bubblewrap (`bwrap`) for sandboxed provider execution;
- one of the supported provider CLIs only when that provider is used; and
- Go 1.25 or newer only when building from source.

MARSHAL does not bundle Bubblewrap, provider CLIs, provider credentials, or
Ollama models.

## First run

Run MARSHAL inside an existing Git repository:

```bash
cd /path/to/repository
marshal init
marshal doctor
```

`marshal init` creates project defaults when they are absent and initializes
the mode-`0700` `.marshal/` runtime directory. It preserves existing regular
project policy/version files and rejects symlinks in their place.

Start the daemon in another terminal:

```bash
cd /path/to/repository
marshal daemon
```

Then import a task and register an agent:

```bash
cat > tasks.json <<'JSON'
[
  {
    "id": "TASK-DEMO-001",
    "title": "Add a repository status check",
    "status": "ready",
    "risk": "R1"
  }
]
JSON

marshal task import tasks.json
marshal agent register --name local-developer --role developer
marshal status
marshal task show TASK-DEMO-001
```

Provider execution is explicit:

```bash
marshal doctor --probe-providers
marshal run TASK-DEMO-001 --adapter codex
```

Use `opencode`, `gemini`, or `claude` as the adapter name for the other
implemented providers. Provider execution can still be denied by policy,
missing credentials, missing sandbox support, or unavailable enforceable
network isolation.

## Provider status

“Implemented” means the current runtime has an adapter and tests for its
process contract. It does not mean the provider binary, credentials, or remote
service were available during this release.

| Provider | Runtime adapter | Local probe in v1.0.1 release validation | Authenticated E2E in v1.0.1 release validation |
|---|---:|---|---|
| Codex (`codex`) | Yes | Reported in release notes | NOT_RUN |
| OpenCode + Ollama (`opencode`) | Yes | Reported in release notes | NOT_RUN |
| Gemini CLI (`gemini`) | Yes | Reported in release notes | NOT_RUN |
| Claude Code (`claude`) | Yes | Reported in release notes | NOT_RUN |

External-provider tests are opt-in because they require installed binaries,
valid credentials or a running local service, and provider access. Skipped
tests are never counted as E2E verification.

## How the runtime fits together

```text
CLI / MCP / A2A / Web
          |
          v
       Runtime
          |
   capability + policy
          |
 network decision + sandbox
          |
       provider
          |
 evidence + task outcome
          |
 canonical SQLite memory
```

The runtime recalls bounded, scope-authorized memory before provider execution
and captures deterministic evidence-linked candidate memory after completion.
SQLite is canonical; lexical, vector, and graph indexes are disposable
projections. Memory records carry project/task/principal scope, provenance,
lifecycle, authority, conflict, freshness, and retrieval-receipt data. See
[Runtime memory](docs/runtime-memory-fabric.md) for the implemented lifecycle.

Resource Awareness takes bounded point-in-time measurements. `marshal doctor`
shows the summary, and authenticated Web users can inspect the same class of
data through `GET /api/v1/resources`. Shared or unknown integrated-GPU memory
is reported as `SHARED_OR_UNKNOWN`, not fabricated as zero. See
[Resource Awareness](docs/resources.md).

## Security boundaries

- The daemon uses a mode-`0600` Unix socket under a mode-`0700` project runtime
  directory.
- Bubblewrap isolation is fail-closed for tasks that require it. Unsandboxed
  process-only execution is disabled by default and can only be enabled for
  low-risk R0/R1 work.
- Network policy is deny-by-default. Bubblewrap can disable networking but
  cannot enforce a host/port allowlist by itself, so endpoint-restricted egress
  fails closed until an enforcing proxy is available.
- Capabilities, role authority, task leases, path validation, evidence
  sanitation, and memory scope checks are enforced independently of provider
  output.
- The Web UI binds to `127.0.0.1` by default and uses one-time login codes,
  session cookies, CSRF protection, CSP, and route-level authorization.
- Release archives have SHA-256 checksums, an SPDX SBOM, a release manifest,
  and GitHub build-provenance attestations.

Read [SECURITY.md](SECURITY.md) and the
[security model](docs/security-model.md) before enabling provider execution.

## Web UI

After `marshal init`, start the loopback control plane:

```bash
marshal web serve
```

The command prints a short-lived, single-use login URL. A production CLI start
requires the canonical runtime; it never silently falls back to demo data.
Canonical memory, resource, backup, health, task, run, evidence, and audit
surfaces are available where backed by current runtime state. Fixture-only
overview/provider-routing surfaces return `501 Not Implemented` with a clear
error when a live runtime is attached; Community does not claim adaptive
provider routing.

## Known limitations

- v1.0.1 does not claim an authenticated external-provider E2E run; see the
  release notes for exact `PASS` and `NOT_RUN` results.
- Endpoint-specific provider egress is unavailable without an enforcing
  proxy, so those requests fail closed.
- Linux Bubblewrap is the supported sandbox backend. There is no equivalent
  production sandbox claim for macOS or Windows.
- Some Web panels remain explicit test/demo fixtures and are disabled for a
  live runtime rather than returning invented production state.
- Optional vector retrieval requires a configured local provider. Canonical
  SQLite, exact/lexical retrieval, scope enforcement, and receipts do not.
- Automated security suites cover tested invariants; they are not a substitute
  for an independent third-party audit.

## Documentation and support

- [Getting started](docs/getting-started.md)
- [CLI reference](docs/cli.md)
- [Architecture](docs/architecture.md)
- [Providers](docs/providers.md)
- [Memory](docs/runtime-memory-fabric.md)
- [Resource Awareness](docs/resources.md)
- [Troubleshooting](docs/troubleshooting.md)
- [Contributing](CONTRIBUTING.md)
- [Support](SUPPORT.md)

## License

The Community edition is licensed under
[AGPL-3.0-only](LICENSE). Separate commercial terms are described in
[LICENSING.md](LICENSING.md) and [COMMERCIAL-LICENSING.md](COMMERCIAL-LICENSING.md).
Third-party attributions are listed in
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
