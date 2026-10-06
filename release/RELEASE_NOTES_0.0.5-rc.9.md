# MARSHAL v0.0.5-rc.9 — The Marshal workspace

A release candidate for 0.0.5. rc.8 was tagged but not published because a sandbox test could not see the release runner's Python; rc.9 fixes that test. It contains everything in rc.7 plus the credential broker: governed Codex and Claude Code workers can use your subscription sign-in without seeing the token.

## What changed

**The Marshal is at the centre of a tmux workspace.** Plan with the Marshal,
keep the work in view, and switch between conversations without leaving the
workspace. Codex, Claude Code, OpenCode and Antigravity can work side by side
in four agent sessions. Choose which agent to focus on or show beside MARSHAL.

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

- Constitution enforcement: 0.3%
- Scope of protection: 6.8%

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

- **Real-provider end-to-end testing is pending.** Automated tests use
  simulated agents. A full run from planning through approval to accepted work
  has not yet been completed with real providers.

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
