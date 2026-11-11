# Providers and Harness Verification

Marshal orchestrates multiple AI coding agents across your project. Instead of relying on a single provider for planning, execution, and verification, Marshal coordinates different CLIs to ensure balanced, independently verified work.

> [!WARNING]
> **Host Execution Notice**: Worker processes execute directly on your host machine with your user privileges and without a sandbox. Ensure you trust the goals and instructions given to each provider.

---

## Supported Providers

Marshal supports four runtime providers out of the box:

| Provider | Binary | Integration Role | Authentication / Setup |
|---|---|---|---|
| **Claude Code** | `claude` | Marshal, Worker, Reviewer, Verifier | Authenticate via native Anthropic CLI (`claude login`) |
| **OpenAI Codex** | `codex` | Marshal, Worker, Reviewer, Verifier | Authenticate via native OpenAI CLI (`codex`) |
| **OpenCode** | `opencode` | Worker, Reviewer, Verifier | Configure provider keys in OpenCode configuration |
| **Antigravity** | `agy` | Marshal, Worker, Reviewer, Verifier | Authenticate via native Antigravity CLI (`agy login`) |

Marshal uses the CLIs already installed on your system. It does not bundle agent binaries, proxy credentials, or manage API billing.

---

## Harness Verification

Before assigning tasks to an agent provider, Marshal runs a **harness verification probe**.

### What Verification Proves

The harness probe tests the provider binary to verify:
1. **Binary Availability**: The command is present on `$PATH` and executable.
2. **Interactive & Headless Protocol**: The provider can accept prompts, run non-interactively, and emit structured output.
3. **Probe Integrity**: The model responds with the exact verification handshake string (`MARSHAL_PROBE_OK`).
4. **Tool Support**: File manipulation and Git command capabilities operate properly.

If a probe times out, exits with a non-zero status, or produces invalid output, the provider is marked `unverified` or `limited` and will not be assigned tasks until re-verified.

---

## How to Verify Providers

You can verify providers through several interfaces:

### 1. On the Board (TUI)

- **First-Use Card**: When opening a fresh project, press **`v`** to arm verification for the next unverified provider shown in the vitals row. Press **`Enter`** to run the probe.
- **Providers View**: Press **`4`** to switch to the Providers view, select a provider with `j` / `k`, and press **`v`** then **`Enter`** to verify it.

### 2. From the TUI Command Line

Type `/` on the Board to open the command prompt:

```text
/harness verify codex
/harness verify claude claude-3-7-sonnet
/harness verify-native agy
/provider status
```

### 3. From the Terminal (CLI)

Run diagnostics before opening the TUI:

```bash
# Run comprehensive checks including provider probes
marshal doctor --probe-providers

# Probe an individual provider adapter
marshal adapter probe agy
marshal adapter probe codex
marshal adapter probe claude
marshal adapter probe opencode
```

---

## Model Selection and Configuration

Marshal allows you to configure which models each provider uses:

```text
# Show current model selection
/model show

# Select a model for a specific harness
/model select claude claude-3-7-sonnet
/model select codex o3-mini
/model select agy gemini-3.8-flash-high

# List available models
/models
```

---

## Handling Provider Limits and Failover

If an AI provider encounters rate limits, billing caps, or network errors during a run:
1. Marshal records the failure reason in `.marshal/state.db`.
2. The affected provider lane is marked as limited.
3. If an alternate verified provider is available, Marshal reassigns pending tasks according to the approved plan.
4. If no eligible provider remains, the run pauses with an orange **`NEEDS YOU`** alert on the Board, allowing you to re-verify, select a different model, or resolve the quota before resuming.
