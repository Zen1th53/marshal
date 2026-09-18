# MARSHAL v0.0.3 — Native Antigravity, Guided Setup and Verified Updates

v0.0.3 adds Antigravity's CLI as a native session alongside Codex, Claude and
OpenCode, takes an empty directory to a ready project from one command, and lets
MARSHAL tell you when a newer release exists and install it on request.

## Highlights

- **Native Antigravity sessions.** Open the Antigravity CLI (`agy`) with
  `marshal agy`, `/agy` or `F12`, using your own configuration and sign-in.
  `/agy continue`, `/agy resume <conversation>`, `/agy cli <args>` and
  `/agy <prompt>` map onto agy's own flags. When agy exits, its visible
  conversation, tool calls and command output are saved to project memory;
  the model's reasoning is never read. Its work reaches the other agents'
  briefings, and theirs reaches agy through `AGENTS.md`. The Team panel now
  finds `agy` instead of reporting it unavailable.
- **Guided setup.** `marshal setup` now offers the blocking steps it reports,
  in the order they have to happen: initialize a Git repository, make an empty
  first commit as a baseline, and set up MARSHAL for the project. Each step is
  asked for by itself and runs only on a yes. `marshal init` no longer has to be
  typed separately.
- **Update notice in the workspace.** When a newer release is published, the
  activity panel says so, with the key that installs it:
  `Update  MARSHAL v0.0.4 is available  [F10] Download and install · /update`.
- **`/update` and `marshal update`.** Check for a newer release from the
  workspace or the shell, and install it with `/update install`,
  `marshal update install` or `F10`.
- **Security and reliability improvements.**
  - Updates are installed only after the archive matches the release's
    published SHA-256. A download that fails verification is not installed,
    and the binary in place is left untouched.
  - The new binary is put in place by a rename within its directory, so it is
    never half-written.
  - Checking and installing are separate. The workspace checks the release
    feed on its own, but installs nothing until you press `F10` or run the
    install command, and `F10` installs only the release the notice is showing.
  - The release source is fixed to this repository and cannot be redirected.
  - `setup` changes nothing without an answer: where its output is not going
    to a terminal, and for `setup status`, it only reports. MARSHAL's automatic
    repair set is unchanged.
  - agy's conversation databases are opened read-only. Its storage format is
    not published, so only fields observed to carry visible conversation and
    tool evidence are read, and a step that cannot be parsed is skipped rather
    than guessed at. Only conversations this launch created or continued are
    imported, and one recorded against another workspace is left to it.
  - The first commit `setup` makes is empty, so no file in the directory is
    added to the repository without your decision.
  - Git's "Author identity unknown" and its revision-parsing error on a
    repository with no commits are now reported in plain terms with the step
    to take.
  - Handoff checkpoints and rollbacks are ordered by time rather than by the
    text of their timestamps. Trimmed fractional seconds made "…:07Z" sort
    after "…:07.5Z", which could return the wrong checkpoint as the latest and
    made a store test fail intermittently.

## Configuration

- `MARSHAL_NO_UPDATE_CHECK=1` stops MARSHAL contacting the release feed at all.

## Installation

Install the latest checksum-verified Linux release:

```bash
curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh | sh
```

Pin this release explicitly:

```bash
MARSHAL_VERSION=v0.0.3 \
  sh -c "$(curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh)"
```

From v0.0.3 on, `marshal update install` upgrades an existing installation.

Published assets include Linux amd64 and arm64 archives, SHA-256 checksums, an
SPDX SBOM, a release manifest and GitHub build-provenance attestations.

## Verification

- Antigravity: decoding of user input, visible answers, tool calls, command
  output and failures with reasoning excluded; workspace attribution; a
  real-terminal session with memory capture and `F12`; and an end-to-end run
  with agy 1.2.5 whose conversation was found in MARSHAL memory after exit
- Setup: real-terminal tests for each offered step, a declined step, a run with
  no terminal, and `setup status`
- Update: verified install, refused install on a checksum mismatch, version
  comparison, and the opt-out variable
- Update against the published feed, and a real install from v0.0.1 to v0.0.2
- Workspace notice, `F10` behaviour and command registration tests
- Checkpoint ordering across timestamps that differ only in trimmed fractions
- Full internal package and TUI test suites
- Release workflow build, test, race, vulnerability, conformance, clean-install
  and manifest gates before publication
