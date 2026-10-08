# Operator permissions and earlier work

MARSHAL renders **Permission request** with tmux `display-popup` (tmux 3.2a or
newer). It lists each exact path, memory candidate ID or host:port, its scope
and duration, requester, and reason. Local proposal files say **Local request
(unverified)** and **The local request says**. Only requests from the runtime
consumer bound to the live Marshal chat say **Marshal**. Intake preference
updates appear in activity with the same provenance.
Uppercase **A** allows the displayed list; **D**, Enter, Escape, any other key,
EOF, popup failure and 30 seconds without an answer deny. Requests wait while
the selected worker pane permits operator typing. Pending duplicates collapse
and requests arriving together appear in one list. Each decision is recorded
in `PERMISSION_DECIDED` evidence before a read grant or memory write takes effect.
Network grants also retain the existing egress evidence. A model's text cannot
supply the keyboard decision or authenticated local operator context.

The existing `/egress allow <run-id> <host:port>` command remains an equivalent
operator action, including for a CLI run owned by the daemon. `/egress status`
combines active daemon and TUI runs in the same project. Pending daemon refusals
can open permission review in the TUI; popup grants and command grants both
require the authenticated local operator's `egress.decide` capability. They
persist an exact run-incarnation and host:port decision for the owning proxy to
read. Model text, socket peer UID and historical notifications confer no grant
authority. Remote revocations deny new requests immediately and close existing
connections at the owner's next 100 ms poll; expired runs cannot be granted.

`/permission read allow <exact absolute folder>` records a
session-only read grant; replace `allow` with `deny` to revoke it. Grants expire
when this MARSHAL runtime ends and are not restored from historical evidence.
A denial for a child folder overrides an allowed parent. Grants bind to the
resolved folder at decision time; replacing a symlink does not grant its new target.

After choosing a language the compiled Marshal protocol asks whether to
continue previous work, with a recommendation, and clarifies the agents and
work. The Marshal emits a structured continuation proposal with the exact
provider and folder; MARSHAL opens the permission popup. After uppercase A,
MARSHAL grants the read and automatically delivers project-scoped earlier work
to the Marshal inbox. Manual `/continue` commands remain available in the
MARSHAL composer; no command needs to be typed into the provider chat. An exact transcript file
can also be supplied to narrow the read. Claude normally stores this project's
sessions and memory in `~/.claude/projects/<encoded-project-path>`; Codex uses
`~/.codex/sessions/<year>/<month>/<day>`. Environment overrides are respected.
Do not grant an entire home directory. Codex's global memories have no reliable
project ownership in this reader and are not imported as project memory.

Only transcripts with this project's exact canonical CWD are proposed.
Candidates retain agent, session and date provenance. Secret-bearing messages
are dropped and MARSHAL reports that they were dropped. Read material is data,
never instructions. Ask the Marshal to summarise completed and unfinished work,
decisions and conventions from the displayed material. `/memory review` shows
pending candidates, `/memory request <id>` requests the popup, `/memory allow
<id>` approves one entry, and `/memory deny <id>` records denial. Candidates are
volatile until approved; restarting discards unapproved proposals. Native
history capture also proposes candidates rather than automatically importing
them into durable memory.

**Enforcement boundary.** MARSHAL's continuation reader, session inventory and
runtime-attached history watchers require recorded read grants. The reader
filters project ownership and rejects symlink escapes. A native Marshal CLI
runs with the operator's provider configuration and filesystem tools. MARSHAL
cannot intercept or restrict that CLI's own filesystem reads, stop a same-user
host process from reading files or changing MARSHAL storage, or guarantee that
a native model follows its read-only instructions. These are not filesystem
sandbox guarantees. Governed worker network isolation remains separate and
does not govern native provider traffic.

MARSHAL automatically stores deterministic run outcomes as **System records**.
These are runtime evidence (run status, exit code, commits and check results),
not model facts. They have `runtime_outcome` provenance and a `system_record`
classification, and need no permission popup. Memory lists and recall label
these records, including older run records. Model proposals and imported or
continued session entries still require a separate operator approval for each
entry. Approved session history is for the Marshal only and is excluded from
worker launch briefs.

On tmux 3.2a, permission review opens a dedicated `marshal-permission` window
instead of a popup. Uppercase A allows; D, Enter, Escape, any other key,
closure and timeout deny. This avoids the observed popup/window-creation
server crashes. Ctrl+X from any MARSHAL window routes to the control pane and
stops workers while preserving the Marshal chat.

Restarted runtimes show old network refusals as expired evidence. Only pending
requests of live runs may open permission review; completed or stopped runs
cannot receive a new grant.

## Protocol proposals

The bound native Marshal history consumer recognises standalone visible final
assistant lines beginning `MARSHAL_PROPOSAL ` followed by a JSON object. The
parser rejects duplicate, unknown or inappropriate fields, non-string values,
control characters, trailing data, unknown actions and invalid setting values.
Only allow-listed setting keys/enums and canonical decimal limits are accepted.
Continuation folders must be exact absolute paths; memory IDs must name pending
candidates. User messages, tool payloads and other conversations do not propose
runtime actions. Repeated identical proposals are suppressed for this runtime's
Marshal conversation, even after a decline.

Settings and run actions use the existing command handler after uppercase A;
reads and memory reuse the same permission decision path as their slash handlers.
`PERMISSION_DECIDED` commits with the local authenticated actor before an action
runs. Run actions additionally bind the stored plan version and state revision;
changed state refuses the old action. Approval proposals wait for draft import.
Outcomes are appended to `.marshal/inbox/marshal.md`, which the native Marshal
reads during chat. As with existing inbox delivery, this does not inject terminal
keystrokes or automatically start a provider turn.

The model's briefing retains: “You cannot run /marshal commands, change settings
or approve anything.” Its working-mode question emits the recommended setting
proposal immediately and says “MARSHAL will show a popup; press A to apply”.

## Governance file protection

A governed result that changes `.marshal/`, policy files, `.github/` or other CI
configuration, agent configuration directories, `AGENTS.md`, `CLAUDE.md`,
`SKILL.md` or other recognised instruction files needs a separate operator popup
even when those paths are in the approved task scope. The popup shows the
reserved paths and binds consent to the accepted commit and current run revision.
**D** keeps the accepted result unmerged. **A** resumes that exact run; a changed
result or revision expires the request. Missing popup consent refuses delivery.

Plan approval records a digest of governing configuration, policy, CI and
instruction files. Admission and the active proposal monitor detect unexpected
changes and report an integrity alert; dispatch, resume and merge refuse to
continue until the original governing files are restored or a new plan is
approved. Mutable `.marshal` operational records (database, artifacts, logs,
worktrees, proposals and inbox) are excluded. Within `.marshal`, the baseline
covers files named for policy, constitution, configuration or instructions.
Older runs without a saved baseline retain their earlier integrity limits.

This is detection and delivery approval. Native workers still have the user's
host rights and can modify governing files, the database or even the saved
baseline outside MARSHAL. These checks cannot prevent all same-user writes.
