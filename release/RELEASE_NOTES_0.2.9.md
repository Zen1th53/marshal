# Marshal v0.2.9

## Fixed

- **Marshal sets up a new project folder by itself.** If your home folder is
  a Git repository (common if you keep your dotfiles in Git), running `marshal`
  or `marshal init` in a folder inside it used to stop with "Marshal refuses
  your home folder as a project root". Now Marshal starts a separate Git
  repository in that folder, makes a first commit of what the folder holds and
  carries on: "Started a new Git repository for this project in
  ~/Desktop/testM and committed 12 files (left out: .env)." Secrets and
  dependencies stay out of that commit: a folder without a `.gitignore` gets a
  short default one (`.env`, keys, `node_modules/`, `.venv/`, build output and
  the like), an existing `.gitignore` is kept, and files that look like secrets
  by name (`.env*`, `*.pem`, `*.key`, `id_rsa*`, `*.p12`, `credentials*.json`)
  are never committed; they stay where they are, untracked. The same happens in a folder that is in no Git repository
  at all. Your home folder's repository is never touched and no file is moved.
  The first commit uses your own Git name and email; if Git does not know them
  yet, Marshal stops and shows the two `git config --global` commands to run.
- **Setup no longer says READY in the wrong place.** `marshal setup` in a
  folder inside your home folder's repository now says so and what to do,
  instead of reporting the project as ready.
- **Doctor gives one reason, not seventeen failures.** When there is no
  project to check, `marshal doctor` prints one line saying why and what to do
  next, and one line for the checks that need a project. `marshal doctor --json`
  is unchanged.
- Running Marshal directly in your home folder or `/` now says plainly: "Marshal
  does not use your home folder (or /) as a project. Make a folder for your
  project, cd into it and run marshal there."

- **The Marshal tells you when a run is done.** When a run closes, the
  Marshal now says in the chat what was delivered and asks what you want next,
  instead of still saying the work is underway.
- **No internal IDs in your Git history.** Commits Marshal makes in your
  repository no longer carry run or operation IDs, `marshal init` tells you
  which files it added and committed, and when a run finishes Marshal also
  removes branches left by task attempts that were sent back and redone (any it
  cannot remove are named in Activity).
- **A second Marshal window opens reliably.** Opening a second window on the
  same project no longer sometimes stops with "Some of Marshal's files in this
  project are damaged" while the first window is starting or closing.
- **Clearer Board.** Each requirement on the requirements card is readable in
  full; a task's files read "calc.py, test_calc.py"; during the last step the
  header says "final check: re-running the checks on the merged result"; a
  usage reading shows its age only when it is over 30 minutes old ("read 2h
  ago"), the same in the vitals row and the provider pickers.
- A brand-new project no longer says the Marshal "did not review a memory
  imported from an earlier chat": the Marshal's own conversation is no longer
  imported as memory.
- Worker windows no longer open with "error retrieving current directory"
  lines: Marshal now starts its terminal server from a folder that is never
  deleted.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh | sh
```

Existing installs: press `F10` in the TUI or run `marshal update install`.
