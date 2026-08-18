# MARSHAL documentation

This portal separates operator guidance from implementation design. Start with
[Getting started](getting-started.md); consult reference documents when you
need exact commands or contracts.

## Start

- [Installation](installation.md) — supported platforms, binary and source install
- [Getting started](getting-started.md) — initialize a repository and run a task
- [Troubleshooting](troubleshooting.md) — diagnostics and common failure modes
- [Examples](../examples/README.md) — checked-in runnable inputs

## Concepts and architecture

- [Architecture](architecture.md)
- [Runtime model](runtime.md)
- [Execution cells](execution-cells.md)
- [Dynamic task DAG](dynamic-dag.md)
- [Multi-agent scheduler](multi-agent-scheduler.md)
- [Events](events.md) and [provenance](provenance-chain.md)

## Interfaces

- [CLI reference](cli.md)
- [Provider adapters](providers.md)
- [OpenCode with Ollama](providers/opencode-ollama.md)
- [MCP](mcp.md)
- [A2A](a2a.md)
- [Policy as code](policy-as-code.md)

## Security and trust

- [Security model](security-model.md)
- [Security reporting](../SECURITY.md)
- [Capabilities](capability-broker.md)
- [Agent roles](agent-roles.md)
- [Network egress](network-egress-firewall.md)
- [Secrets](secrets.md)
- [Supply chain](security/supply-chain.md)

## Operations and development

- [Upgrade and migration](operations/upgrades.md)
- [Release process](development/release-process.md)
- [Contributing](../CONTRIBUTING.md)
- [Support](../SUPPORT.md)

Files under `docs/superpowers/` are retained engineering design records. They
are not operator instructions or a product roadmap.
