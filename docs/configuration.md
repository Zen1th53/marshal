# Configuration

Marshal provides project-level configuration files, in-session TUI settings, CLI flags, and environment variables.

> [!WARNING]
> **Host Execution Notice**: Worker processes execute directly on your host machine under your user account without a sandbox. Worktrees isolate Git changes, not system calls or process execution. The Marshal plans and approves the work itself; accepting the architecture is your one gate, so review it before you accept, and stop the run at any time with `Ctrl+X`.

---

## In-Session Settings

You can inspect and update orchestration settings directly from the Board command line by typing `/`:

### Viewing Settings

```text
/marshal settings
```

Settings shows configured and effective values, units, scope, source, and when a change applies. The browser Settings page shows the same project rows and the latest 100 change records. Each project change commits its actor, old and new values, event ID, and revision with the settings revision check. Existing approved runs keep their approved snapshot. A key without a value shows usage and never writes.

User defaults use `/marshal settings <key> <value>` too:

- `update-check`, `web-auto-open`, `thermal-enabled`, `display-animation`: `on|off`.
- `thermal-trigger` and `thermal-release`: degrees Celsius; release must be below trigger.
- `thermal-window`: 1–60 seconds.
- `display-theme`: `default|monochrome|high-contrast|no-color`.

These defaults apply at the next launch and share one atomic user record with their change history. Explicit launch flags and environment preferences take precedence. `MARSHAL_NO_UPDATE_CHECK`, `MARSHAL_WEB_OPEN=0`, and `MARSHAL_CLOUD_ENDPOINT=off` retain their opt-outs.

Project file preferences are `notifications` (`on|off`), `session-import` (`on|off`, applied at launch), and `memory-channel` (the same values as `/memory inject`). Session import never grants permission to read history. These files accept earlier one-line values and save value and audit history together. The fixed policy rows describe the current build and cannot be edited here.

Run money ceilings use minor currency units; goal money ceilings use USD. Goal ceilings belong to an approved goal revision. Unknown usage remains unknown and does not bypass quota.

### Changing Settings

```text
/marshal settings control strict
/marshal settings rework-limit 3
/marshal settings acceptance-mode user
```

`/marshal settings <key>` with no value shows the choices, units and when a change applies; it never writes. Each setting says when it applies in the **Applies** column of the tables below: most apply to the **next run**, and provider switches and failover order apply at the **next admission**. The Marshal chat reads `execution-rights` when it launches. An approved run keeps the snapshot it was approved with.

In the default edition the Marshal can also change the settings listed in its proposal schema (`acceptance-mode`, `execution-rights`, `control`, `rework-limit`, `ultra-concurrency` and the six budget ceilings) when you ask it to, without a separate confirmation step. In the governed edition the Marshal's proposal opens a popup and nothing changes until you press `A`. The audit and activity trail record every change.

### Settings Reference

The tables below are generated from the settings catalogue (`go run ./tools/feature-inventory/cmd/settingsdocs -write docs/configuration.md`, once for each edition) and are checked in CI. Defaults are the effective values a new project uses. Two editions are built from this repository: the **default** edition, and the **governed** edition (`-tags governed`).

#### Default edition

<!-- settings-table:default:begin -->

| Setting | Choices or units | Default | Applies | Notes |
|---|---|---|---|---|
| `execution-rights` | read-only, small-tasks | `read-only` | next run | Approved and running settings keep their snapshot. |
| `acceptance-mode` | marshal, marshal-then-user, user | `marshal` | next run | Approved and running settings keep their snapshot. |
| `control` | free, strict | `free` | next run | Approved and running settings keep their snapshot. |
| `cross-review` | required, off | `required` | next run | Controls optional independent hand-in cross-review for upcoming runs. Omission is recorded per attempt; mandatory architecture verdicts remain required. |
| `design-review` | on-demand, required, off | `on-demand` | next run | Controls pre-approval independent design critique in a fresh session bound to the draft digest. |
| `architecture-conformance` | enforce, warn, off | `enforce` | next run | Controls hand-in architecture conformance (enforce, warn, or off). Plans with a mandatory architecture obligation lock conformance to enforce. |
| `final-model` | required, off | `required` | next run | Locked: canonical completion requires the independent verifier. Canonical completion requires the independent verifier; no settings edit alone authorizes delivery. |
| `rework-limit` | attempts | `2` | next run | Approved and running settings keep their snapshot. Zero allows no rework attempts. |
| `ultra-concurrency` | workers | `4` | next run | Inactive legacy setting kept for older projects: it does not set how many workers run. Use the provider limits and global-cap. |
| `worker-stall-minutes` | minutes | `15` | next run | Approved and running settings keep their snapshot. Zero or off disables the stall watchdog while keeping observation signals; blank restores 15 minutes. |
| `codex-enabled` | on, off | `on` | next admission | Controls worker eligibility for this provider. Running workers finish; changes apply at the next admission. |
| `claude-enabled` | on, off | `on` | next admission | Controls worker eligibility for this provider. Running workers finish; changes apply at the next admission. |
| `agy-enabled` | on, off | `on` | next admission | Controls worker eligibility for this provider. Running workers finish; changes apply at the next admission. |
| `opencode-enabled` | on, off | `on` | next admission | Controls worker eligibility for this provider. Running workers finish; changes apply at the next admission. |
| `task-tokens` | tokens | `0` | next run | Approved and running settings keep their snapshot. Zero leaves this ceiling unlimited. |
| `plan-tokens` | tokens | `0` | next run | Approved and running settings keep their snapshot. Zero leaves this ceiling unlimited. |
| `task-money` | minor currency units | `0` | next run | Approved and running settings keep their snapshot. Zero leaves this ceiling unlimited. |
| `plan-money` | minor currency units | `0` | next run | Approved and running settings keep their snapshot. Zero leaves this ceiling unlimited. |
| `task-wall-seconds` | seconds | `0` | next run | Approved and running settings keep their snapshot. Zero leaves this ceiling unlimited. |
| `plan-wall-seconds` | seconds | `0` | next run | Approved and running settings keep their snapshot. Zero leaves this ceiling unlimited. |
| `codex-limit` | workers | `4` | next run | Approved and running settings keep their snapshot. |
| `claude-limit` | workers | `2` | next run | Approved and running settings keep their snapshot. |
| `agy-limit` | workers | `4` | next run | Approved and running settings keep their snapshot. |
| `opencode-limit` | workers | `4` | next run | Approved and running settings keep their snapshot. |
| `global-cap` | workers | `12` | next run | Approved and running settings keep their snapshot. |
| `failover-order` | string | `codex,claude,agy,opencode` | next admission | Controls provider failover order for new dispatches, reassignments and quota failovers. |
| `infra-retry-limit` | attempts | `6` | next run | Approved and running settings keep their snapshot. Zero makes infrastructure retries unlimited; blank restores 6. |
| `infra-failover-streak` | failures | `2` | next run | Approved and running settings keep their snapshot. Zero disables failure-streak cooldown; blank restores 2. |
| `infra-cooldown-seconds` | seconds | `600` | next run | Approved and running settings keep their snapshot. Zero disables provider cooldown after failure streaks; blank restores 600 seconds. |
| `verification-timeout-seconds` | seconds | `0` | next verification | Zero leaves repository verification unlimited; blank uses the unlimited default. |

<!-- settings-table:default:end -->

#### Governed edition

<!-- settings-table:governed:begin -->

| Setting | Choices or units | Default | Applies | Notes |
|---|---|---|---|---|
| `execution-rights` | read-only, small-tasks | `read-only` | next run | Approved and running settings keep their snapshot. |
| `acceptance-mode` | marshal, marshal-then-user, user | `marshal-then-user` | next run | Approved and running settings keep their snapshot. |
| `control` | free, strict | `free` | next run | Approved and running settings keep their snapshot. |
| `cross-review` | required, off | `required` | this build | Locked: review policy is locked in this edition. Approved and running settings keep their snapshot. |
| `design-review` | on-demand, required | `on-demand` | next run | Controls pre-approval independent design critique in a fresh session bound to the draft digest. Disabling design review (off) is locked in this edition. Value off is locked: weakening review policy is locked in this edition. |
| `architecture-conformance` | enforce, warn, off | `enforce` | this build | Locked: review policy is locked in this edition. Approved and running settings keep their snapshot. |
| `final-model` | required, off | `required` | this build | Locked: review policy is locked in this edition. Canonical completion requires the independent verifier; no settings edit alone authorizes delivery. |
| `rework-limit` | attempts | `2` | next run | Approved and running settings keep their snapshot. Zero allows no rework attempts. |
| `ultra-concurrency` | workers | `3` | next run | Maximum number of workers at once while ULTRA execution is on (otherwise one at a time). Tasks of an approved governed plan still dispatch one at a time. |
| `worker-stall-minutes` | minutes | `none` | this build | Locked: not used in this edition. Approved and running settings keep their snapshot. |
| `codex-enabled` | on, off | `on` | next admission | Controls worker eligibility for this provider. Running workers finish; changes apply at the next admission. |
| `claude-enabled` | on, off | `on` | next admission | Controls worker eligibility for this provider. Running workers finish; changes apply at the next admission. |
| `agy-enabled` | on, off | `on` | next admission | Controls worker eligibility for this provider. Running workers finish; changes apply at the next admission. |
| `opencode-enabled` | on, off | `not available` | this build | Locked: not available in this edition. Controls worker eligibility for this provider. Running workers finish; changes apply at the next admission. |
| `task-tokens` | tokens | `0` | next run | Approved and running settings keep their snapshot. Zero leaves this ceiling unlimited. |
| `plan-tokens` | tokens | `0` | next run | Approved and running settings keep their snapshot. Zero leaves this ceiling unlimited. |
| `task-money` | minor currency units | `0` | next run | Approved and running settings keep their snapshot. Zero leaves this ceiling unlimited. |
| `plan-money` | minor currency units | `0` | next run | Approved and running settings keep their snapshot. Zero leaves this ceiling unlimited. |
| `task-wall-seconds` | seconds | `0` | next run | Approved and running settings keep their snapshot. Zero leaves this ceiling unlimited. |
| `plan-wall-seconds` | seconds | `0` | next run | Approved and running settings keep their snapshot. Zero leaves this ceiling unlimited. |
| `codex-limit` | policy | `not available` | this build | Locked: not configurable in this edition. Workers use the run concurrency limit. |
| `claude-limit` | policy | `not available` | this build | Locked: not configurable in this edition. Workers use the run concurrency limit. |
| `agy-limit` | policy | `not available` | this build | Locked: not configurable in this edition. Workers use the run concurrency limit. |
| `opencode-limit` | policy | `not available` | this build | Locked: not configurable in this edition. Workers use the run concurrency limit. |
| `global-cap` | policy | `not available` | this build | Locked: not configurable in this edition. Workers use the run concurrency limit. |
| `failover-order` | policy | `not available` | this build | Locked: not configurable in this edition. Workers use the run concurrency limit. |
| `infra-retry-limit` | attempts | `0` | this build | Locked: not available in this edition. Infrastructure retry is not available in this edition. |
| `infra-failover-streak` | policy | `not available` | this build | Locked: not available in this edition. Automatic provider failover is not available in this edition. |
| `infra-cooldown-seconds` | policy | `not available` | this build | Locked: not available in this edition. Automatic provider failover is not available in this edition. |
| `verification-timeout-seconds` | seconds | `900` | this build | Locked: fixed at 15 minutes in this edition. Repository verification stays fixed at 15 minutes (900 seconds) in this edition. |

<!-- settings-table:governed:end -->

Notes that apply to both tables:

- `execution-rights` accepts `none` as an older spelling; it is stored and behaves as `read-only`. `small-tasks` lets the Marshal make small changes itself.
- `acceptance-mode` defaults to `marshal` in the default edition and to `marshal-then-user` in the governed edition.
- A value locked by the edition is shown in its row and refused with the reason.

### Concurrency

How many workers run at once depends on the edition.

- **Default edition**: with ULTRA execution on, the caps are the per-provider limits (`claude-limit`, `codex-limit`, `agy-limit`, `opencode-limit`) and `global-cap`. With ULTRA execution off or not entitled, one worker runs at a time. `ultra-concurrency` is kept so older projects still load, but it does not set how many workers run: use the provider limits and `global-cap`. `antigravity-limit` is the same setting as `agy-limit`.
- **Governed edition**: provider limits, `global-cap` and `failover-order` cannot be configured (`/marshal settings claude-limit 4` is refused as not configurable in this edition). `ultra-concurrency` (default 3) is the number of workers allowed at once while ULTRA execution is on (otherwise one at a time), and governed tasks of an approved plan dispatch one at a time.

```text
/marshal settings claude-limit 4
/marshal settings global-cap 8
/marshal settings failover-order codex,claude,agy,opencode
```

`0` returns a limit to its default. `failover-order` takes provider names separated by commas, spaces or `->`. To keep work away from a provider, switch it off with its `<provider>-enabled` setting. The Web Board shows the limits in **DEFAULTS · this project** and in the Lanes title (`limit claude 4 · agy 4 · total 12`).

### Budgets and Units

There are two separate budget systems:

- **Run ceilings**, set with `/marshal settings`: `task-tokens`, `plan-tokens` (tokens), `task-money`, `plan-money` (minor currency units, such as cents) and `task-wall-seconds`, `plan-wall-seconds` (seconds). Zero leaves a ceiling unlimited. When a token or money ceiling is set and a worker's usage is unknown, the run pauses instead of continuing, because unknown usage is not treated as zero.
- **Goal budget**, set with `/budget set [calls=<n>] [duration=<go duration>] [tokens=<n>] [cost=<n>] [handoffs=<n>] [reserve=<n>]`. Goal ceilings belong to an approved goal revision and use USD for money. `/budget` shows the current budget.

Other units: `rework-limit` counts attempts, and the thermal preferences above are degrees Celsius and seconds. In the default edition the worker stall window is `worker-stall-minutes` (default 15; `off` or `0` disables the watchdog); the governed edition does not use it.

### Operating Mode and ULTRA

`/mode` shows or sets the supervision mode (`manual`, `auto`, `ultra`); it grants no authority, and hard approvals always stay with you. ULTRA needs a verified entitlement. `/ultra status|start|stop` shows, turns on or turns off ULTRA execution for the project.

### Other Controls

- `/notify on|off|status` controls attention notifications (the `notifications` project preference above).
- `/memory inject [channel]` sets how a native session receives the other agents' work; see [Memory and MCP](memory-and-mcp.md).
- `marshal telemetry status|enable|disable` controls anonymous usage counts; `MARSHAL_CLOUD_ENDPOINT=off` disables cloud communication entirely.
- Project setting changes made from the Web Settings page or the command line record the actor, old and new values and revision in the settings change history.

---

## Project Files

When Marshal sets up a project (on first open, or with `marshal init`), it creates standard configuration files in your repository root:

| File | Location | Purpose |
|---|---|---|
| `CAPABILITIES.yaml` | Project root | Project capability declarations and default permissions. |
| `PACK-VERSION.yaml` | Project root | Version specification of the project pack. |
| `RUNTIME-VERSION.yaml` | Project root | Minimal runtime version requirements for the repository. |
| `.marshal/` | Project root (ignored) | Project runtime directory containing `state.db` (SQLite store), task logs, and session data. |

> [!NOTE]
> Project setup excludes `.marshal/` from Git tracking by appending it to `.git/info/exclude`, leaving your `.gitignore` untouched.

---

## Command-Line Flags

Marshal CLI commands accept global flags:

| Flag | Description |
|---|---|
| `--json` | Output command results as structured JSON (suitable for scripting and automation). Can be placed before or after subcommands. |
| `-h`, `--help` | Display command usage and flag descriptions without executing side effects. |

`marshal tui` also takes `--theme THEME`, `--no-color` and `--no-animation`; they override the saved display defaults for that launch.

Example:

```bash
marshal --json status
marshal --json memory list
```

---

## Environment Variables

| Variable | Description |
|---|---|
| `MARSHAL_NO_UPDATE_CHECK=1` | Suppress automatic online checks for new Marshal versions. |
| `MARSHAL_PROVIDER_PATH_ONLY=1` | Restrict agent CLI discovery strictly to directories listed in `$PATH`. |
| `MARSHAL_CLOUD_ENDPOINT` | Specify the community cloud endpoint (default: `https://marshal.blackhat.uz`), or set to `off` to disable telemetry and cloud communication entirely. |
| `MARSHAL_INSTALL_DIR` | Target installation folder when running `install.sh` (default: `$HOME/.local/bin`). |
| `MARSHAL_VERSION` | Specific release tag to install with `install.sh`. |
| `MARSHAL_NO_MODIFY_PATH` | `1` stops `install.sh` from adding the install folder to a shell startup file. |
| `MARSHAL_INSTALL_DEPS` | `0` stops `install.sh` from installing a missing Git or tmux; it only reports them. |
| `MARSHAL_INSTALL_YES` | `1` answers yes to `install.sh`'s questions (sudo still needs a terminal). |
| `MARSHAL_WEB_OPEN` | `0` keeps the browser closed when the Board starts: the Web Board still starts, and `w` or `/marshal web` opens it. Unset (the default) opens it in your browser where there is a display. |
| `MARSHAL_NO_USAGE_PROBES` | Any non-empty value stops Marshal from probing provider usage; admission usage is then unknown, and unknown usage never bypasses quota. |
| `MARSHAL_REDUCED_MOTION` | Any value other than empty or `0` turns Board animation off. |
| `MARSHAL_OPENCODE_MODEL` | The model Marshal uses for OpenCode, including its harness verification. |
| `MARSHAL_CODEX_TRUST_PROFILE` | Default edition: `off` stops Marshal from adding its trust profile to Codex launches. |
| `BOARD_TRUECOLOR` | `1` draws the Board in 24-bit colour, as `COLORTERM=truecolor` or `24bit` does, for a terminal that takes 24-bit colour but does not set `COLORTERM`. |
