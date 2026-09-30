# MARSHAL v0.0.5-rc.4 — Marshal mode

A release candidate. It is cut from an integration branch, not from `main`: the
branch merges the open pull requests #24–#31 and #35–#39 onto v0.0.4, in their
stack order, then #40 and #46, and none of them is merged into `main` yet.

## Since rc.3

Found in live testing of rc.3, all in #46:

- **Enter respects the completion you picked.** Choosing `settings` from the
  `/marshal` menu with the arrows or Tab and pressing Enter used to open a
  Marshal session. Enter now accepts the highlighted candidate; with nothing
  picked, it still runs a finished command such as `/goal `.
- **`/marshal` on its own no longer opens a session.** It shows the Marshal's
  status and the command list. `/marshal chat` opens the conversation.
- **A mistyped subcommand runs nothing.** `/marshal setings` answers "Did you
  mean /marshal settings?" instead of starting a planning run with the typo as
  its goal. A goal of more than one word still starts planning.
- **Malformed commands are refused.** `/marshal status now` or
  `/memory inject auto extra` show the usage instead of acting.
- **The `/marshal` menu lists every subcommand**, `chat` first.
- **`/memory inject` knows all four agents.** It reports, previews and
  confirms the channel for Claude, Codex, OpenCode and Antigravity (`agy`), and
  works, like `/memory peers`, without a database.
- **No hint for what you cannot use.** The activity panel and `/help` show the
  navigation shortcuts only when navigation is open to the session, and the
  Tab and Enter help matches what the keys do.

## In rc.3

Found in live testing of rc.2, all in #40:

- **The Marshal no longer prints its protocol.** Every agent reads its briefing
  where it reads instructions:
  - Claude from the system prompt;
  - Codex from developer instructions;
  - OpenCode and Agy from a per-session directory under `.marshal/briefing/`.

  You see the MARSHAL wordmark, a short "Begin." and the Marshal's
  introduction.
- **Claude as the Marshal works.** Its protocol was dropped whenever the
  cross-agent memory reached the system prompt first. A second briefing now
  joins the first.
- **MARSHAL stays out of your repository.**
  - Briefings are no longer written into the project's `AGENTS.md` or
    `CLAUDE.md`.
  - A block an earlier version left there is removed on the next launch.
  - The briefing directory ignores itself in git and is deleted when the
    session ends.
- **The Marshal introduces itself.** It states its model, that it is MARSHAL's
  Marshal here, and whether the run is Standard or ULTRA. It opens with some
  swagger, then stays plain.
- **The protocol follows the agreed business process end to end.**
  - Each planned task says why its worker was chosen and what it should
    produce.
  - The plan table shows the control level.
  - The final report lists every criterion as verified or not tested, the
    budget spent, remaining risks and what was not done.
- **A MARSHAL wordmark plays while the Marshal starts**, a different one each
  time. The launch line and the Marshal panel no longer name the provider
  behind the Marshal.

Install it deliberately. `install.sh` and `/update` follow the latest release,
and a candidate is not that.

## What changed

**One model plans with you, then marshals the work to other agents.**

`/marshal chat` opens a conversation with the appointed Marshal model (#24). It
follows a fixed protocol, compiled into the binary and pinned by a digest (#31,
#35):

1. it introduces itself;
2. it asks which language to work in;
3. it asks your goal;
4. it reads the current state of the project before asking anything else;
5. it asks what the plan depends on, one question at a time, each with a
   recommendation.

It records nothing you did not answer.

**What you agree stays written down** (#35). When the Marshal is done, it writes
a plan pack: `REQUIREMENTS.md`, `00_INDEX.md` and one `tasks/<id>.md` per task.
After `/exit`, MARSHAL moves the pack to `.marshal/marshal/runs/<run>/plan/` and
shows where it is. You can read it and correct it. `/marshal approve` binds the
pack as it stands into the approval, and every worker's brief carries its own
task note and the requirements.

**Workers run the approved plan, and nothing else.**
- After approval, each task runs in its own worktree, and MARSHAL re-runs the
  task's checks itself (#24).
- Dependent tasks start from the merged work they depend on. A reassigned
  worker starts fresh from the task's base (#28).
- Choose the control level before approval (#29):
  - `/marshal settings control strict`: every task carries instructions that
    the worker follows exactly;
  - `free`: the worker chooses its approach within the task's files.
- In user and hybrid acceptance modes, `/marshal return <task> <reason>` sends
  work back (#27).
- Every reviewing role runs in a session of its own, so a single installed
  provider is enough, ULTRA included (#25).

**ULTRA Execution is switched on in the TUI** (#38). The
`MARSHAL_ULTRA_EXECUTION` variable is gone:
- every session starts with execution off;
- `/ultra start` turns execution on when this installation holds a verified
  entitlement;
- `/ultra stop` asks first, and `/ultra stop confirm` turns execution off.

Commands outside the TUI, such as `marshal goal`, always ask you.

**`/ultra` says more** (#36, #37):
- it shows when the grant ends, separately from the five-minute session lease;
- a refusal from the Community Cloud shows the server's reason instead of a
  bare status code.

**One Cloud identity per machine** (#30). The installation identity now lives in
`~/.config/marshal/cloud_state.json` instead of in every project, and MARSHAL
reports its real version to the Cloud.

**The Ctrl+N navigation surface is closed** (#39). It still has screens with no
capability behind them, and no end-to-end run has shown that it works. Until
that is proven, Ctrl+N and Esc refuse for every session. Everything remains
available from the composer.

## Known limits

- **Not run with real models end to end.** Codex, Claude and Agy have each
  been started as the Marshal and followed the protocol's opening. None has
  yet taken a plan from `/marshal chat` through approval to accepted work.
- **OpenCode is a worker, not a Marshal model.** The Marshal's review and
  verification turns need schema-enforced output, which OpenCode does not
  offer. OpenCode receives its briefings, but a small local model may not
  follow them.
- **Keep the window open for a run.** MARSHAL has no background service, so the
  window must stay open while a run is in progress.
- **Older identity carried over.** On first start, a machine that already had a
  project-level Cloud identity adopts it. Check `/ultra` if your entitlement
  seems to be missing.

## Verification

- The release gate (`scripts/community-release-gate.sh`) passed on this commit
  before tagging.
- The release workflow publishes the artifacts with checksums, an SBOM, a
  release manifest and build provenance.
