# MARSHAL Documentation Portal

Welcome to the official MARSHAL documentation portal. Use this index to navigate specifications, guides, CLI references, and security architecture.

---

## Getting Started

- [Getting Started Guide](getting-started.md) — Quick start tutorial from zero setup to first task execution
- [Installation Guide](installation.md) — Binary downloads, building from source, system dependencies
- [CLI Reference](cli.md) — Full command-line usage reference for `marshal`

---

## Core Architecture & Concepts

- [Architecture Overview](architecture.md) — System design, control plane boundaries, and component roles
- [Core Concepts](concepts.md) — Task leases, capability brokers, execution cells, and verification quorum
- [Runtime Specification](runtime.md) — Daemon lifecycle, Unix socket protocols, and SQLite schema (`v69`)
- [Execution Cells](execution-cells.md) — Process isolation, `bubblewrap` sandboxing, and worktrees

---

## Security & Governance

- [Security Model](security-model.md) — Fail-closed sandboxing, secret redaction, and boundary invariants
- [Policy-as-Code Engine](policy-as-code.md) — Security rules, capability broker, and approval gates
- [Network Egress Firewall](network-egress-firewall.md) — Egress policy filtering and socket isolation
- [Risk Engine](risk-engine.md) — Task risk classification (`R0`..`R3`) and approval requirements
- [Security Gates](security-gates.md) — Gate evaluation, risk floors, and security officer vetoes

---

## Provider Adapters & Integrations

- [Provider Support Guide](providers.md) — Capability matrix, provider probing, and maturity states
- [Codex Adapter](providers/opencode-ollama.md) — OpenAI Codex execution configuration
- [OpenCode + Ollama Adapter](providers/opencode-ollama.md) — Local LLM execution with Ollama models
- [MCP Protocol Guide](mcp.md) — Model Context Protocol (2026-07-28) server integration & Bearer auth
- [A2A Protocol Guide](a2a.md) — Agent-to-Agent 1.0 wire protocol specification

---

## Legal & Provenance

- [Legal Audit Guide](legal/IP-PROVENANCE-AUDIT.md) — Chain-of-title tracking, copyright attribution, and verification
- [License History](legal/LICENSE-HISTORY.md) — AGPL-3.0 and historical Apache-2.0 release grants
- [Commercial Licensing](../COMMERCIAL-LICENSING.md) — Commercial license terms for enterprise deployments

---

## Operations & Troubleshooting

- [Troubleshooting Guide](troubleshooting.md) — Diagnostics, common errors, and recovery steps
- [Conformance Suite](conformance.md) — Verification runner and test suite validation
