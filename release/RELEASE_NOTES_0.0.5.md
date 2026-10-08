# MARSHAL v0.0.5 — The Marshal workspace

- Marshal protocol decisions use validated JSON handoffs in `.marshal/proposals`, keeping proposal payloads out of chat. Uppercase A applies allow-listed proposals through existing handlers with operator evidence; Esc, D, n, other non-navigation keys or timeout decline. Identical proposals deduplicate while pending and can be emitted again after a decision.

- Marshal proposals appear on the attached client in the control centre or Marshal chat, even when earlier-history access was declined. F7/F8/F9/F11/F12 navigate without deciding; the request stays pending, and A records one decision. The centre also displays the pending request.
- Governed work waits for an undecided credential popup and continues automatically after A; denial or timeout stops it.
- Reopening or switching the Marshal continues in the saved language and skips answered intake across providers; reopening resumes its known conversation.
- Native operator terminals use the hosted tmux pane dimensions at startup when readable, fall back to 120×40 while unavailable, and keep propagating real sizes to the provider PTY and SIGWINCH.
- Network permission items use compact lines and content-sized popups bounded by the terminal; batches that do not fit require a separate visible popup for each request before approval.
- Earlier-history popups wait for an explicit yes to continuation and focus on the control centre or Marshal chat, so they do not interrupt provider input or trust prompts.

One workspace for planning with the Marshal and working with your agents.

## What changed

- F7/F8/F9/F12 reopen ended provider sessions from the control centre, Marshal chat and native windows.
- F2 and `/review` default to uncommitted changes and explain when the working tree is clean.
- Native commands with arguments are refused with retry instructions when that provider already has a session; arguments no longer disappear into its composer.
- OpenCode help and completion omit unrouted plugin commands; `/opencode run` requires text.
- Marshal controls explain how to start a run when chat has no saved plan, instead of showing an internal storage error.
- `/help all` starts at the beginning of the full reference; PgUp/PgDn page it and End jumps to the end.
- Empty-run pause/resume/cancel messages and approval help are corrected. `/permission status` explains the available next steps. Ordinary native exits report session status without a blank worker run alert.
- The welcome screen and `/help`/F1 lead with `/marshal chat`; `/help all` opens the full reference, and chat openings omit provider qualification labels.
- Setup acknowledges successful Git initialization and presents the first commit as the next step.
- Plan instructions require confirmed writes of the plan pack and task list before read-back.
- Completed governed task panes show a readable worker status, summary and evidence location while retaining full relay output in evidence.

**The Marshal is at the centre of a tmux workspace.** MARSHAL opens on the
control centre, and the Marshal chat stays open in its own window. Plan with
the Marshal, keep the work in view, and switch between conversations without
leaving the workspace. Codex, Claude Code, OpenCode and Antigravity can work
side by side. Choose which agent to focus on or show beside MARSHAL; F11
returns to the control centre from any window. Leaving the Marshal chat with
`/exit` closes it and returns you to the control centre; `/marshal chat`
reopens it. If the chat stops unexpectedly, it restarts and resumes.

**The TUI stays responsive.** Commands, function keys, completion and
navigation never wait on tmux, provider tools, Git or the project store, and
stop-all, F11 and `/takeover` never wait behind a running command.

**Sessions you open are yours to type in.** Native sessions you open with
F7, F8, F9, F12 or `/codex`, `/claude`, `/opencode`, `/agy` accept keyboard
input immediately. Workers the Marshal launches stay view-only until
`/takeover`. A provider key whose session has ended reopens it through the normal provider launcher.

**Choose the Marshal's model.** `/marshal model <codex|claude|agy>` switches
the running Marshal and remembers the choice for the project.

**The Marshal plans protected work.** Each planned task shows whether it runs
natively or governed before you approve it. Codex and Claude Code tasks are
governed by default and run in the sandbox with the egress proxy; OpenCode
tasks can be governed on request (set `MARSHAL_OPENCODE_MODEL` to pick a
model). If protection is unavailable, the task is refused rather than run
natively. A finished `marshal run` task can be reviewed and merged with
`/marshal import <task> <check>`.

**Governed workers can use your subscription without seeing it.** With the
credential broker, governed Codex and Claude Code workers use your ChatGPT or
Claude sign-in through MARSHAL; the worker only ever holds a placeholder.

**The Marshal's instructions stay private.** The Marshal protocol reaches the
model through hidden instructions; the chat shows only a short opening.

**The agents have a shared channel.** They can share notes and read what the
others have contributed. You control sharing. Sharing sends text across
providers when another agent reads it; keeping the channel's records on your
machine does not make that exchange local-only.

**Governed network access goes through an egress proxy.** Each run has its own
allowed endpoints. A request for another endpoint is refused and shown to you.
Only the authenticated local operator can allow it with `/egress allow`, or
withdraw access with `/egress revoke`. An agent can relay a request, but cannot
approve it. If network enforcement is unavailable, the work is refused.
Native sessions you open directly use their provider's own permission controls.

**A honeypot watches governed work.** Synthetic credentials help detect an
agent trying to use or expose secrets. A detected hit stops the task and keeps
evidence for review; the task must not be merged.

**An empty folder can become a project.** Run `marshal init` in a terminal.
It guides you through creating Git and an initial commit before adding
MARSHAL's settings. Existing project files are preserved. Non-interactive
initialisation still needs a Git repository with a baseline commit.

**Security boundaries are tighter.** This release strengthens filesystem path
and directory handling, host Git command execution, approval identity and
binding checks, network confinement, and the handling of credentials and
evidence. These protections apply at their supported boundaries; native agent
sessions retain the rights of your user account.

## Requirements

- **Linux only**, on 64-bit Intel/AMD or ARM. macOS is unsupported; WSL 2 has
  not been tested.
- **tmux 3.3a or newer** is required for the TUI.
- **socat** is required for governed work with network access. That work is
  refused without it.
- **Bubblewrap** provides governed worker isolation. Missing isolation can
  prevent work from running.

After installing, `install.sh` prints one clear line for each missing tmux or
socat, with the install command for the detected pacman, apt, dnf, zypper or
brew package manager. It does not install dependencies automatically, and a
missing dependency does not make installation fail.

## Protection coverage

The automated source inventory reports:

- Constitution enforcement: 0.6%
- Scope of protection: 7.0%

Constitution enforcement is the percentage of material source call sites that a constitutional verdict can refuse before the effect occurs.
Scope of protection is the percentage of material source call sites guarded before execution by a constitutional gate, approval binding, policy authorisation or sandbox.
Both use the same inventory of Git mutations, material program and agent launches,
restores, material record writes and listeners; read-only queries, path resolvers
and diagnostic probes are excluded.
Percentages are truncated to one decimal, never rounded up.

These are code-site counts, including wrapper dispatch and lower-level
boundaries. General helpers with unguarded paths are counted as unguarded;
recording a verdict or warning does not count as enforcement. The reviewed
[site table](../tools/effect-inventory/sites.csv) and
[counting rules](../tools/effect-inventory/README.md) make the scope inspectable.

## Known limits

- **Keep the window open.** There is no background service. Stored runs remain
  recoverable with `/marshal resume` after reopening MARSHAL.
- **Credential broker is opt-in per project and provider.** Use
  `/permission credential request <codex|claude|gemini|opencode>` and press
  uppercase `A` in the fixed permission prompt. Revoke with
  `/permission credential revoke <provider>`. Supported API keys stay on the
  host; the worker gets only placeholders and a public run CA. Injection is
  limited to the selected provider HTTPS host; other allowed hosts remain
  CONNECT tunnels. Setup failures refuse work without copying native auth.
  Codex ChatGPT subscription sign-in works for governed workers through the
  broker; the worker never sees the token. Each request reads host sign-in afresh.
  A bounded host CLI account refresh owns persistence; MARSHAL never writes that
  file, serializes refreshes, and retries a 401 once. The managed-auth protocol
  was verified locally for Codex 0.160.1; a Linux VM test completed
  governed Codex and Claude tasks through the broker.
  Claude Code subscription sign-in also works for governed workers through the
  broker. It reads host sign-in afresh and never sends the refresh token. At
  expiry or a 401 it re-reads once and retries only with a changed fresh token;
  otherwise it refuses the request and alerts once per run. Run `claude` once
  on the host to refresh it, then retry. Codex refresh failures, incompatible
  formats, and commands exceeding 20 seconds refuse work. The broker does not prevent approved
  workers from spending provider quota or sending permitted data to it.
  See [broker details](../docs/public/credential-broker.md) for profiles and
  response buffering limits.
- **OpenCode is a worker, not the Marshal.** Planning needs Codex, Claude Code
  or Antigravity.
- **Scope checks only warn.** Out-of-scope changes and goal drift are recorded;
  they do not stop the task.
- **The Marshal can read and write the whole project.** Protection is at
  approval and merge; a task's file list does not restrict the Marshal's access.
- **Project directories must stay stable while applying work.** MARSHAL cannot
  protect delivery if another process moves or replaces those directories.
- **Native workers have your account's rights.** Governed workers are
  sandboxed; a separate working copy alone does not provide that protection.
- **Shared text can reach another provider.** Check what you share before the
  receiving agent uses it.
- **Provider qualification is limited to specific versions.** Other versions
  may use unqualified pass-through commands.
- **Network-dependent tasks may be blocked** when their access cannot be
  safely controlled.
- **Live backup restore is Linux-only.** Elsewhere, restore from the terminal.
- **Ctrl+N navigation screens are switched off.** Use composer commands.

See [What MARSHAL cannot do yet](../docs/public/limits.md) for the full limits.

## Verification

The release preparation checks build and vet all Go packages, exercise the
installer with offline fixtures, and check the effect inventory against its
reviewed table and these notes.
