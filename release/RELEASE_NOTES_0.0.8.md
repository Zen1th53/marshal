# MARSHAL v0.0.8

Evidence and verification integrity.

## What changed

### Approval and acceptance binding

- Task acceptance binds the current hand-in attempt, result commit, evidence digest
  and plan version. Queued work cannot receive advance consent; `/marshal accept`
  identifies the exact result it accepts.
- Plan approval displays the pack digest, repository, base commit and target ref.
  Changed pack bytes require review and approval again.
- Automatic delivery needs a separate confirmation for the shown target branch.
  Close refuses a changed repository, base or target. Scoped amendments explicitly
  cancel standing delivery when the approval digest changes and ask again;
  unchanged digests keep consent.

### Evidence and verification

- Acceptance checks test read-only source with separate writable build space.
  Hand-ins record the result commit and tested tree digest.
- Failed mandatory evidence, claim or journal capture leaves the task failed with
  an "incomplete evidence" reason and preserves its work.
- Independent review references must resolve to hand-in artifacts. Verifier
  identity, commit, verdict, findings and input binding are retained and shown in
  `/marshal status`.
- Acceptance and completion reports share approved-check aggregation: any
  applicable failure fails the criterion; mixed and incomplete evidence is visible.
- Governed hand-ins require a recorded clean honeypot scan. A recorded hit
  quarantines the commit at merge admission, including after restart. Missing
  scan identity refuses delivery.
- Tasks declare change, inspection or verification. Change tasks require changed
  files; unchanged inspection and verification results need complete passing
  evidence.
- Process 05 scope and explicit constraints are checked against the recorded base
  and fail the task; semantic goal drift remains an advisory warning.
- Completion reports label Standard review as the Marshal's own ("Standard: review
  by the Marshal itself; independent review in ULTRA") and confirm approved checks
  passed.

### Governance integrity

- Constitutional suspension and stored version bindings now refuse dispatch and
  resume; unreadable bindings fail closed.
- Governed results that change policy, CI, MARSHAL storage or agent instructions
  need separate, commit-bound operator popup consent before merge.
- Governing-file digests detect unexpected changes and show an activity alert.
- Local file proposals say **Local request (unverified)**; intake preference
  updates show their source in activity.
- Governed requests refuse native tasks in drafts unless the operator explicitly
  allows an exception.

### Durable lifecycle

- Lifecycle intents and completion records survive interruptions. Startup
  reconciles launches, hand-ins, merges and closes without repeating completed
  effects; run, task, hand-in and audit updates commit together. Plan revisions
  and run state also commit together.
- Replacement plans use separate branches and worktrees and preserve earlier
  evidence.
- Pending proposals return to the popup queue after restart, and resolved
  occurrences stay resolved.
- The Marshal validates and reads back its plan before publishing the completion
  marker.
- `/marshal resume` retries failed verification and reports the reason and next
  step when a pause needs operator action.
- Escalated tasks support operator `/marshal retry`, `/marshal reassign` and
  `/marshal cancel` where permitted; single-provider tasks support bounded
  fresh-session retries.
- Reopening Marshal chat after a closed or finished run starts fresh with a
  new run ID and watches for new plan drafts and approval proposals.

### ULTRA

- ULTRA work requires both entitlement and execution enabled, safely falling back
  to Standard otherwise.
- ULTRA worker hand-ins are collected and reviewed as soon as ready, merging
  sequentially in plan order.

### Credentials and A2A

- Credential revocation reaches every owning runtime through the project store,
  refuses new broker requests immediately, and closes active exchanges. The
  permission command reports pending until owners acknowledge closure; use
  `/permission credential status <provider>` to check it.
- A2A construction requires a configured authentication manager. Unauthenticated
  task import requires explicit insecure construction or `--insecure`, with
  a literal loopback listen address and loopback peers.

### Version display

- CLI and TUI show the same build-injected release version. Local builds show
  `dev`; schema and constitution versions retain their separate labels.

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

- Constitution enforcement: 0.9%
- Scope of protection: 7.4%

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

- Runs approved before destination binding was introduced need a replacement
  plan and fresh approval before delivery. Unbound standing consent is refused.
- **Checks cannot write to tested source.** Commands producing binaries, generated
  files or reports must write them under `$MARSHAL_BUILD_DIR` or `/tmp`; build
  caches have separate writable space. Checks needing unavailable isolation fail.
- **Recovery requires honeypot assurance.** A recovered governed result with a
  missing scan identity is refused; its branch remains available for inspection.
- **Standard review remains in the Marshal conversation.** Independent cross-review
  and model verification are required for ULTRA.
- **Keep the window open.** There is no background service. Unfinished lifecycle
  operations are reconciled when the project reopens. `/marshal resume` retries a failed verification. Budget and security pauses
  require the resolution shown in the panel; uncertain interrupted effects
  retain their artifacts for operator review.
- **A2A authentication is required by default.** Missing authentication configuration
  refuses task import. Deliberate unauthenticated serving requires `--insecure`
  and a literal loopback listen address; non-loopback peers are refused.
- **Credential broker is opt-in per project and provider.** Use
  `/permission credential request <codex|claude|gemini|opencode>` and press
  uppercase `A` in the fixed permission prompt. Revoke with
  `/permission credential revoke <provider>`. New broker requests are refused
  immediately; active exchanges close when their owning runtime consumes the
  decision (normally within 100 ms). Check `/permission credential status <provider>`
  for pending or acknowledged closure. An unavailable owner or failed evidence
  write leaves closure pending; regranting cannot preserve an old exchange. Supported API keys stay on the
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
- **Scope is checked after the worker runs.** Out-of-scope changes and broken
  explicit constraints fail the task, but native workers can still write outside
  their scope on disk before the check. Goal drift is recorded as a warning and
  does not stop the task. Marshal hand-ins validate the approved file list, and
  reserved governed changes need separate consent before merge.
- **The Marshal can read and write the whole project.** Protection is at
  approval and merge; a task's file list does not restrict the Marshal's access.
- **Project directories must stay stable while applying work.** MARSHAL cannot
  protect delivery if another process moves or replaces those directories.
- **Native workers have your account's rights.** Same-user native writes to
  governance files and MARSHAL storage cannot be fully prevented. Reserved-path
  merge approval and governing-file integrity detection are additional checks,
  not host isolation. Mutable operational records are excluded from the digest;
  runs created before this release have no integrity baseline. Governed workers
  are sandboxed; a separate working copy alone does not provide that protection.
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
