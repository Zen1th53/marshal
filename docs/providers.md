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
/harness verify-native agy        # auto: the best model agy offers
/harness verify-native agy fast
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
/model select agy best

# List available models
/models
```

### Antigravity: name a need, not a model

agy is never tied to one model. Give Marshal a selector and it picks the
exact model from what your signed-in agy offers right now (`agy models`), so a
new model is used as soon as your plan has it, with no Marshal update:

| Selector | Picks | Example (Google AI Ultra, 19 November) |
|---|---|---|
| `auto` (or nothing) | the Orchestrator with the strongest planner (a planner model plans, Hydrogen executes), else `best` | `gemini-pro-orchestrator-neon` |
| `best`, `plan` | newest generation, strongest family, high effort | `gemini-4-neon-high` |
| `fast` | newest fast family (Hydrogen, then Flash), high effort | `gemini-4-hydrogen-high` |
| a family word (`neon`, `argon`, `hydrogen`, `flash`, `orchestrator`, …) | that family's newest model | e.g. `gemini-4-argon-high` |
| `<family>-<effort>` (`neon-xhigh`, `argon-low`, …) | that family at that effort | `gemini-4-neon-xhigh` |
| an exact ID | that model, if agy offers it | `claude-opus-5-5-high` |

A family Marshal does not know yet is taken as the newest, so a model that
appears under a new name (Neon arrived as Carbon left) is preferred without a
Marshal update; an Orchestrator's planner is read from its display name.
On a Pro plan `auto` takes the Argon Orchestrator; with only Flash it takes
Flash. Verification probes the exact model the selector resolved to, and the
evidence records that model.

## Provider versions

Marshal does not keep a list of allowed provider versions. A CLI is used at
whatever version is installed once its probe passes: Marshal runs it, checks
its output, and records the result. When a provider updates itself, Marshal
notices the new version and re-runs the probe on its own, before planning, at
dispatch and in the background, instead of asking you to run
`/harness verify` again. Which commands a provider offers (`/agy mcp add`,
`/claude plugin install`, …) is read from that CLI's own `--help`.

---

## Handling Provider Limits and Failover

If an AI provider encounters rate limits, billing caps, or network errors during a run:
1. Marshal records the failure reason in `.marshal/state.db`.
2. The affected provider lane is marked as limited.
3. If an alternate verified provider is available, Marshal reassigns pending tasks according to the approved plan.
4. If no eligible provider remains, the run pauses with an orange **`NEEDS YOU`** alert on the Board, allowing you to re-verify, select a different model, or resolve the quota before resuming.

## Antigravity as the Marshal

Started with `marshal agy`, Antigravity leads the project with the same tools
as Codex and Claude. agy has no per-session MCP setting, so when the project
has no `.agents/mcp_config.json` of its own Marshal writes one naming only its
own server (no secret: the session's token and lease reach the server through
agy's environment), keeps it out of git and removes it with the session; a
file you own is never touched. The session also starts with
`--settings '{"permissions":{"allow":["mcp(marshal/*)"]}}'`, applied to that
session only, so agy does not ask you to approve each Marshal tool.

## Provider Limits on the Board

The top row shows what each provider reports, never a guess:

- **Codex** — % left and reset time from its last session, marked with its age.
- **Claude** — % left of the 5-hour and weekly windows and when they reset, from
  the `rate_limit_event` Claude Code prints while Marshal runs it.
- **agy** — % left and reset time of each quota group (Gemini, Claude and GPT,
  Gemini 4 Argon; 5-hour and weekly), from `agy -p /usage`. When agy cannot
  answer, the row falls back to which model families it offers and which ones
  your account had are spent (`ok: Flash, Claude · out: Gemini 4`); agy is
  marked out of limit in the planner picker only when no family is offered.
