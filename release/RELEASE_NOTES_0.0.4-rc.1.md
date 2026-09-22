# MARSHAL v0.0.4-rc.1 — One shared channel

A release candidate. It carries the cross-agent channel and nothing else: it is
cut from the branch that work lives on, not from `main`, so the four other
changes open at the time are not in it.

Install it deliberately. `install.sh` and `/update` follow the latest release,
and a candidate is not that.

## What changed

**Every agent drops its work into one ordered channel, as it happens.**

Cross-agent exchange used to be point to point: a running session copied what it
did into every other agent's mailbox. Four agents meant four copies of one
event, a guard against writing it twice, and a table of who posts to whom —
bookkeeping the shape created rather than the problem.

There is one channel now. What each agent sees is decided when it *reads*: a
filter naming the authors it takes, and a cursor saying how far it has looked.

```
.marshal/stream/events.jsonl   the channel — ordered, append-only
.marshal/stream/cursors.json   how far each agent has looked
.marshal/inbox/<agent>.md      that agent's view of it
.marshal/live-peers            who joins, and who each agent sees
```

**An agent joining late joins the conversation.** It used to start from empty,
on the reasoning that its launch briefing had already summarised what came
before. A summary is not the work. A reader now drains from its cursor, so an
agent opening while another is five steps into a task sees those five steps.

**An agent that was closed still catches up.** Entries accumulate whether or not
the recipient is running. Each carries the time it happened, and a boundary
marks where a session begins, so nothing older reads as though it just arrived.

**All four agents now reach the channel while they work.** Claude Code and Codex
write append-only history. Antigravity and OpenCode keep SQLite, opened
read-only and never written to; SQLite serves readers under WAL while a writer
holds the file. OpenCode was the last exception and it was an inconsistency
rather than a limit — its private store is no more private than agy's, which was
already being read.

Reading a store MARSHAL does not own is a coupling, and it is guarded. OpenCode's
shape is checked before each read; if it has moved, that path stands down and
the supported CLI export takes over, which cannot run mid-session and so
delivers at exit — later than it should be, never wrong. What is read is
assembled into the same document the export produces and handed to the same
decoder, so the live path cannot select different fields. **The model's hidden
reasoning is excluded there, once, for both** — checked against 199 real
reasoning blocks, none of which reached a transcript.

## Choosing who sees whom

Two decisions, in `.marshal/live-peers` or through `/memory peers`:

```
participants: claude, codex, opencode, agy
agy: all                  # every other agent
claude: all
codex: opencode, agy      # not claude
opencode: none            # contributes, reads nothing
```

Joining and seeing are separate. An agent can contribute while reading almost
nothing, and that is an arrangement rather than a gap: models differ in what
they can use. A capable one does better seeing everything the others did; a
smaller one does worse, because context it cannot follow is context it can be
confused by.

**An agent is never shown its own work**, and that is not a setting. It already
knows what it did.

## Capture reports itself

The native CLI owns the terminal for a whole session, so MARSHAL cannot draw a
counter — it writes one, to `.marshal/<agent>/live-status.json`: records
imported, entries shown, last sync, and any capture error. Memory capture was
always live; nothing said so.

## Fixes in the workspace

- A command that changes the arrangement now reprints it and marks the row that
  moved, instead of answering with one line and leaving the change off screen.
- The channel report no longer lists an author that never joined, which was a
  promise the channel could not keep.
- Colour marks state; every agent name is the same colour. Column padding is
  computed on the visible text rather than the escape bytes.
- Autocomplete: `/memory` offered two of its five subcommands, argument
  completion offered the parent command's list, and an empty word offered
  nothing. The longest registered prefix now wins, and a trailing space lists
  what the command accepts.

## Known limits

**Delivery is a pull.** A running CLI owns its own input; MARSHAL cannot
interrupt it. The view is a file that is current whenever the agent looks, and
the briefing says so rather than implying the agent is kept in sync.

**This candidate is not `main`.** It does not contain the Gemini provider-surface
change, the CI split, the store test template, or the PTY timing fix.

## Verification

- `go test ./internal/tui/` — the full suite, including the PTY conformance
  tests, passing.
- 65,536 channel arrangements written, read back, and checked to still mean the
  same thing for all sixteen author/reader pairs; every filter a reader can have
  rendered to a real file and read back.
- Verified against the installed CLIs on 2026-09-22 — Claude Code 2.1.278, Codex
  0.155.1, OpenCode 1.18.16, `agy` 1.2.7 — by decoding their real transcripts
  with the production watcher.
