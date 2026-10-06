# What's new in 0.0.5

## Plan with the Marshal

- `/marshal chat` opens a conversation with the Marshal. It asks about your
  goal, reads your project, and writes a plan you can read and edit.
- Agents follow the approved plan. Each task runs in its own copy of your
  project, and tasks that depend on others start from their finished work.
- You choose how strictly agents follow the plan, and who accepts the work.
- Send a task back with `/marshal return` and a note on what to fix.
- One installed agent is enough. MARSHAL gives each role its own session.

## Commands that now work

Many commands that only printed "not available" in earlier versions now work:

- edit your goal and see its history,
- approve or reject requests,
- pause, resume and cancel runs, and set limits on calls and time,
- choose the model and, for Codex, how hard it thinks,
- save, compare and restore checkpoints,
- restore a backup without leaving MARSHAL (Linux),
- see past conversations per project with `/sessions`,
- apply a Codex cloud task and see exactly what changed,
- see staged, unstaged and untracked changes with `/diff`.

## Easier to use

- A typo in a command now does nothing and suggests the right command.
- Enter accepts the highlighted suggestion first.
- `/marshal` on its own shows the status instead of starting a session.

## Credential broker

- Request credential use with `/permission credential request <provider>`;
  uppercase `A` in the fixed permission prompt allows it for this project.
  `/permission credential revoke <provider>` revokes it and closes active
  broker connections. Access is off by default; model messages cannot grant it.
- Supported API keys stay on the host. Workers receive placeholders and a
  public CA unique to the run. Only the selected provider's HTTPS host receives
  the key. Other allowed hosts retain their CONNECT tunnels.
- Codex ChatGPT subscription sign-in works for governed workers through the
  broker; the worker never sees the token. Each request reads host sign-in afresh.
  The host CLI performs serialized refreshes, with one retry after a 401; MARSHAL
  never writes host sign-in files. This needs the managed-auth account refresh
  protocol (verified locally for Codex 0.160.1).
- Claude Code subscription sign-in works for governed workers through the
  broker, with fresh host-file reads and placeholder sandbox sign-in. MARSHAL
  never sends its refresh token or writes its host file. At expiry or a 401 it
  re-reads once and retries only with a changed fresh token; otherwise it refuses
  the request and alerts once per run. Run `claude` once on the host to refresh
  it, then retry. Codex refresh has a 20-second limit. Incompatible formats fail
  closed; live provider behavior remains unverified.
- Provider quota use and permitted data sent by a worker remain your
  responsibility. See [broker details](credential-broker.md) for supported
  profiles, trust settings, and response limits.

## ULTRA

- ULTRA is switched on and off from inside MARSHAL with `/ultra start` and
  `/ultra stop`.
- `/ultra` shows when your access ends and, if access is refused, why.

See [What MARSHAL cannot do yet](limits.md) for the known limits of this
version.
