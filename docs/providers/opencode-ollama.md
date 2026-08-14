# OpenCode & Local Ollama Provider Guide

**Runtime Milestone**: `v0.4.0`
**Client Runner**: OpenCode CLI (`opencode`)
**Local LLM Provider**: Ollama Daemon (`http://localhost:11434`)
**Tested Model**: `qwythos-9b`

---

## 1. Overview

MARSHAL supports executing software engineering tasks locally using OpenCode against Ollama models with **zero external cloud API dependencies**.

- **OpenCode (`opencode`)**: Acts as the local execution agent client that receives task instructions, inspects the workspace, and issues tool calls to modify files.
- **Ollama (`ollama`)**: Serves local neural network models over HTTP (`localhost:11434`).

---

## 2. Prerequisites & Verification

### 2.1 Verify OpenCode Installation
```bash
command -v opencode
opencode --version
```

### 2.2 Verify Ollama Installation & Service
```bash
command -v ollama
ollama --version
ollama list
curl -s http://localhost:11434/api/tags
```

Ensure `ollama` service is running in the background (`ollama serve` or systemd service).

---

## 3. Model Compatibility & Tool Calling

> **Crucial Distinction**: Conversational text generation capability **does NOT** imply tool-calling capability.
> OpenCode requires models that explicitly format and issue structured JSON tool calls to create or modify repository files.

| Model | Family | Tool Calls | Status in MARSHAL |
|---|---|---|---|
| `qwythos-9b` | Qwen3.5 9B | ✅ **Confirmed** | **E2E VERIFIED Default** |
| `qwen2.5-coder:14b` | Qwen2.5 14B | ⚠️ Partial | Returns JSON as text in some revisions |
| `llama3:8b` | Llama 3 8B | ❌ Conversational | Fails to emit tool call schema |

To pull the verified model:
```bash
ollama pull qwythos-9b
```

---

## 4. Executing Tasks with OpenCode & Ollama

### Model Selection via CLI
Pass the `--model` flag directly to `marshal run`:

```bash
marshal run TASK-001 --adapter opencode --model qwythos-9b
```

Or set the environment variable:
```bash
export MARSHAL_OPENCODE_MODEL="ollama/qwythos-9b"
```

### Deep Provider Probe
Verify OpenCode flag compatibility and Ollama endpoint connectivity:

```bash
marshal adapter probe opencode
```

---

## 5. Troubleshooting Local Model Execution

### Symptom: Ollama connection refused
- **Cause**: Ollama daemon is not running on `http://localhost:11434`.
- **Fix**: Run `ollama serve` in a background terminal.

### Symptom: Model missing error
- **Cause**: The specified model is not downloaded locally in Ollama.
- **Fix**: Download the model via `ollama pull qwythos-9b`.

### Symptom: Model returns conversational text instead of editing files
- **Cause**: Selected model lacks OpenCode tool-calling schema support.
- **Fix**: Switch to a tool-call verified model such as `qwythos-9b`.

### Symptom: Task returns `ErrConflict` or "worker produced no commit"
- **Cause**: The model executed without error but produced no git diffs or uncommitted files in the worktree.
- **Fix**: Inspect logs with `marshal logs TASK-ID` to see the LLM's response chain.
