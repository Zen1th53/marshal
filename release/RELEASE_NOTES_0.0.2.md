# MARSHAL v0.0.2 — Native OpenCode and Automatic Session Memory

v0.0.2 adds OpenCode as a first-class native terminal session alongside Codex
and Claude. It also corrects terminal cursor placement for the `❯ ` composer
prompt and hardens native-session memory capture.

## Highlights

- Launch OpenCode directly with `marshal opencode`, `F9`, or `/opencode`.
- Start, continue, resume and fork OpenCode sessions from the MARSHAL TUI.
- Pass native OpenCode arguments through without shell re-parsing.
- Automatically import visible OpenCode conversation and bounded tool evidence
  into project candidate memory when the native process exits.
- Exclude private reasoning, snapshots and provider metadata from memory.
- Preserve normal memory secret scanning before any imported record is stored.
- Feed bounded cross-agent project context and live inbox information into
  native OpenCode sessions.

## Fixed

- The composer keeps the visible `❯ ` separator while Ctrl+Left places the
  hardware cursor on the first input character.
- OpenCode export capture preserves visible conversation instead of persisting
  redacted placeholders.
- Transient partial OpenCode exports retry once.
- Existing OpenCode history is baselined before launch, so exit capture imports
  the session created or updated by that run.
- Stable session record IDs make repeated imports idempotent when provider
  metadata changes.
- Oversized provider JSONL events no longer prevent later conversation from
  being imported.
- Repeated peer-history rejection diagnostics appear once instead of filling
  the native-session result pane.

## Installation

Install the latest checksum-verified Linux release:

```bash
curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh | sh
```

Pin this release explicitly:

```bash
MARSHAL_VERSION=v0.0.2 \
  sh -c "$(curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh)"
```

Published assets include Linux amd64 and arm64 archives, SHA-256 checksums, an
SPDX SBOM, a release manifest and GitHub build-provenance attestations.

## Self-hosted development

This release was developed in MARSHAL's own native-agent workspace. MARSHAL
coordinated Codex and OpenCode sessions in the MARSHAL repository and captured
completed native-session context into its canonical project memory. The final
archives are still produced independently by the pinned release workflow from
the exact annotated tag.

## Verification

- Native OpenCode unit and real-PTY lifecycle tests
- Codex and Claude native-session regression tests
- Session importer, idempotency and secret-boundary tests
- Full internal package and TUI test suites during development
- Release workflow build, test, race, vulnerability, conformance, clean-install
  and manifest gates before publication
