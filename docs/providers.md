# MARSHAL Provider Capability Model & Adapter Guide

**Runtime Milestone**: `v0.4.0`

MARSHAL implements a vendor-neutral provider architecture where AI coding agents interact with the repository through process adapters.

---

## 1. Provider Capability Data Model

MARSHAL distinguishes between binary presence, protocol capability, authentication, and empirical end-to-end verification. A provider's maturity is classified into six distinct states:

| Capability State | Meaning |
|---|---|
| `IMPLEMENTED` | Adapter code and contract execution loop exist in the Go codebase. |
| `INSTALLED` | Provider CLI binary is discovered on `$PATH`, `~/.local/bin`, or `/usr/local/bin`. |
| `AVAILABLE` | Binary executes basic `--version` and `--help` diagnostic probes cleanly. |
| `AUTHENTICATED` | Provider credentials or local daemon connections pass probe verification. |
| `CAPABILITY-PROBED` | Provider flag compatibility and non-interactive parameters are verified. |
| `REAL-E2E-VERIFIED` | Full-chain task execution verified across Native CLI, MCP (2026-07-28), and A2A (1.0). |

---

## 2. Provider Support Matrix

| Provider Adapter | Binary | Current State | Native | MCP | A2A | Notes |
|---|---|---|:---:|:---:|:---:|---|
| **Codex** | `codex` | `REAL-E2E-VERIFIED` | ✅ | ✅ | ✅ | Mandatory Release Gate |
| **OpenCode + Ollama** | `opencode` + `ollama` | `REAL-E2E-VERIFIED` | ✅ | ✅ | ✅ | Mandatory Release Gate (`qwythos-9b`) |
| **Gemini CLI** | `gemini` | `CAPABILITY-PROBED` | — | — | — | API Quota Limited (429) |
| **Claude Code** | `claude` | `CAPABILITY-PROBED` | — | — | — | OAuth Session Expired |
| **Aider** | `aider` | `IMPLEMENTED` | — | — | — | Contract specification |
| **Crush** | `crush` | `IMPLEMENTED` | — | — | — | Contract specification |

---

## 3. Provider Configurations

### Codex Adapter (`codex`)
- **Binary**: `codex` (`codex-cli 0.146.0`+)
- **Lookup Paths**: `$PATH`, `~/.local/bin/codex`, `/usr/local/bin/codex`
- **Execution Command**: `codex exec --json --sandbox --ephemeral --ignore-user-config --cd <WORKTREE>`
- **Verification Status**: `REAL-E2E-VERIFIED`
- **Sandbox Requirement**: Requires `bubblewrap` for fail-closed R1-R3 execution.

### OpenCode + Local Ollama Adapter (`opencode`)
- **Binaries**: `opencode` (`1.18.16`+) and `ollama` (`0.32.6`+)
- **Lookup Paths**: `$PATH`, `~/.local/bin/opencode`, `/usr/local/bin/opencode`
- **Model Backend**: Local Ollama service (`http://localhost:11434`)
- **Tested Model**: `qwythos-9b`
- **Verification Status**: `REAL-E2E-VERIFIED`
- **Key Note**: Text generation capabilities do not imply tool-calling capability. Models must be explicitly tool-call capable to modify repository files.

### Gemini CLI Adapter (`gemini`)
- **Binary**: `gemini` (`0.50.0`+)
- **Verification Status**: `CAPABILITY-PROBED / UNVERIFIED`
- **Limitation**: Unauthenticated or quota-limited (429) environments gracefully skip integration tests without blocking release gates.

### Claude Code Adapter (`claude`)
- **Binary**: `claude` (`2.1.218`+)
- **Verification Status**: `CAPABILITY-PROBED / UNVERIFIED`
- **Limitation**: OAuth session expiration reported truthfully without fake PASS.

---

## 4. Probing Provider Adapters

Inspect available provider binaries and adapter status using the CLI:

```bash
# List provider adapters and binary paths
marshal adapters

# Perform a deep probe against a specific adapter
marshal adapter probe codex
marshal adapter probe opencode
```
