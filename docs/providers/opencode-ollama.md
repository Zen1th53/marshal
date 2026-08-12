# OpenCode & Local Ollama Provider Operational Guide

**Runtime Milestone**: SLAVES Runtime v0.3.0  
**Target Provider**: Local Ollama (`http://localhost:11434`)  
**Target OpenCode**: Stable 1.18.x CLI (`opencode`)  

---

## 1. Overview

SLAVES Runtime v0.3.0 supports executing repository engineering tasks via OpenCode against local Ollama models with zero external cloud dependencies.

---

## 2. Requirements & Discovery

### 2.1 OpenCode Binary
Verify stable `opencode` binary:
```bash
command -v opencode
opencode --version
```

### 2.2 Local Ollama Endpoint
Verify local Ollama service and installed models:
```bash
command -v ollama
ollama --version
ollama list
curl -s http://localhost:11434/api/tags
```

---

## 3. Recommended Models

The following models are confirmed to support OpenCode tool-calling via Ollama:

| Model | Family | Tool Calls | Notes |
|-------|--------|-----------|-------|
| `qwythos-9b` | Qwen3.5 9B | ✅ Confirmed | Default for SLAVES E2E tests |
| `huihui_ai/Qwen3.6-abliterated:27b` | Qwen3.5 27B | ⚠️ Server errors | Requires more RAM |
| `huihui_ai/qwen2.5-coder-abliterate:14b` | Qwen2 14B | ❌ No tool calls | Returns JSON as text |
| `blackarch-ai:latest` | Qwen2 9B | ❌ No tool calls | Returns JSON as text |

> **Note**: The Qwen2 (`qwen2`) family models return tool call JSON as plain text and do not execute tools. Use Qwen3.5 (`qwen35`) family models for real tool execution.

---

## 4. Execution & Model Selection

SLAVES automatically passes non-interactive execution flags to OpenCode:
```bash
opencode run --auto --format json -m ollama/<model_name> "<prompt>"
```

Or set the environment variable:
```bash
export SLAVES_OPENCODE_MODEL="ollama/huihui_ai/qwen2.5-coder-abliterate:14b"
```

---

## 5. Real Verification Gate

To execute the real OpenCode/Ollama E2E verification suite:
```bash
SLAVES_TEST_REAL_OPENCODE=1 go test -v ./internal/integration -run TestRealOpenCode
```
