# MARSHAL v0.0.4 — One shared channel

Agents in a project now work from one ordered channel instead of from a
briefing that went stale the moment it was written. Each agent drops what it
does into it as it works, and each reads its own view of it — filtered by what
you decided before the work started.

## The channel

**Every agent contributes as it works.** Claude Code and Codex write
append-only history that MARSHAL reads as it grows. OpenCode and Antigravity
keep SQLite, opened read-only and never written to, which SQLite serves to
readers while a writer holds the file. No agent waits until it exits.

**An agent joining late joins the conversation.** It used to start from empty,
on the reasoning that its briefing had already summarised what came before. A
summary is not the work. A reader now resumes from its own cursor, so an agent
opening while another is five steps into a task sees those five steps.

**An agent that was closed catches up.** Entries accumulate whether or not the
recipient is running. Each carries the time it happened, and a boundary marks
where a session begins, so nothing older reads as though it just arrived. A
full view drops its oldest entries rather than sealing itself, because the
recent work is the part a returning agent needs.

```
.marshal/stream/events.jsonl   the channel — ordered, append-only
.marshal/stream/cursors.json   how far each agent has looked
.marshal/inbox/<agent>.md      that agent's view of it
.marshal/live-peers            who joins, and who each agent sees
```

## Choosing who sees whom

Two decisions, both made before the work starts, with `/memory peers` or in
`.marshal/live-peers`:

```
participants: claude, codex, opencode, agy
agy: all                  # every other agent
claude: all
codex: opencode, agy      # not claude
opencode: none            # contributes, reads nothing
```

Joining and seeing are separate. An agent can contribute while reading almost
nothing, and that is an arrangement rather than a gap: **models differ in what
they can use.** A capable one does better seeing everything the others did; a
smaller one does worse, because context it cannot follow is context it can be
confused by. The two directions between any pair may disagree — a reviewer can
read the implementer without the implementer reading the reviewer.

**An agent is never shown its own work**, and that is not a setting. It already
knows what it did.

## Capture reports itself

Memory capture was always live; nothing said so, because the only report came
after the agent exited. The native CLI owns the terminal for a whole session,
so MARSHAL cannot draw a counter — it writes one instead, to
`.marshal/<agent>/live-status.json`: records imported, entries shown, last sync
and any capture error.

## Workspace

- **Completion works on arguments.** The menu used to close after a command and
  a space, exactly where you had most reason to expect it, and Tab then took the
  first candidate silently. It now opens for a command's arguments too, and Tab
  steps through the candidates the way a shell does, leaving the menu up.
- **Enter settles, a second Enter runs.** Settling on `/memory` is where you
  reach for Tab again to complete `peers`; running on the same keystroke ran
  something half-written.
- **Antigravity is `Agy cli`** in the menu, after the command you actually type.
- Gemini leaves the provider menu. Its adapter, its doctor probe and
  `Import Gemini JSONL` are unchanged — this narrows what the TUI offers, not
  what MARSHAL can run.

## Known limits

**Delivery is a pull.** A running CLI owns its own input; MARSHAL cannot
interrupt it. The view is a file that is current whenever the agent looks, and
the briefing says so rather than implying the agent is kept in sync.

## Verification

Tested against the installed CLIs on 2026-09-22 — Claude Code 2.1.278, Codex
0.155.1, OpenCode 1.18.16, `agy` 1.2.7 — with real sessions rather than
doubles:

- Each agent was run and asked to emit a marker. The watcher read all four real
  stores and put 22 entries into the channel, with all four markers present.
- Each agent was then asked to read its own view. All four correctly named the
  other three and quoted their markers. None reported its own.
- With `codex: none` set, a real Codex session was run **inside the real TUI**.
  The channel went from 22 entries to 64 with that session's marker in it, while
  codex's own view stayed at its header — none of the other three agents'
  markers reached it. The data was there and it was withheld.

The arrangement space is tested exhaustively rather than by example: 65,536
configurations written, read back and checked to still mean the same thing for
all sixteen author/reader pairs, and every filter a reader can have rendered to
a real file and read back.

## Also in this release

Internal, with no change in behaviour: the CI gate no longer runs the whole
suite twice and is split by cost; the store suite migrates once into a template
instead of 311 times, taking it from 400 seconds to 184 under the race
detector; and the PTY waits scale with the race detector, which had been
failing on a loaded runner and reading as a broken TUI rather than a slow one.
