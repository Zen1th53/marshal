# MARSHAL Runnable Examples

This directory contains verified usage examples for the MARSHAL control plane.

---

## Example Index

1. [01-codex-task.json](01-codex-task.json) — Task schema for OpenAI Codex provider execution
2. [02-opencode-ollama-task.json](02-opencode-ollama-task.json) — Task schema for OpenCode + local Ollama execution
3. [03-mcp-client-config.json](03-mcp-client-config.json) — Model Context Protocol client configuration
4. [04-a2a-agent-card.json](04-a2a-agent-card.json) — Agent-to-Agent discovery card schema

---

## How to Run Examples

### Prerequisites
```bash
marshal init
marshal daemon
```

### Run Example 1 (Codex)
```bash
marshal task import examples/01-codex-task.json
marshal run TASK-CODEX-001 --adapter codex
marshal logs TASK-CODEX-001
```

### Run Example 2 (OpenCode + Ollama)
```bash
marshal task import examples/02-opencode-ollama-task.json
marshal run TASK-OLLAMA-001 --adapter opencode --model qwythos-9b
marshal logs TASK-OLLAMA-001
```
