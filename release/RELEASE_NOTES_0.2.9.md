# Marshal v0.2.9

## Fixed

- **Marshal sets up a new project folder by itself.** If your home folder is
  a Git repository (common if you keep your dotfiles in Git), running `marshal`
  or `marshal init` in a folder inside it used to stop with "Marshal refuses
  your home folder as a project root". Now Marshal starts a separate Git
  repository in that folder, makes a first commit of what the folder holds and
  carries on: "Started a new Git repository for this project in
  ~/Desktop/testM." The same happens in a folder that is in no Git repository
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

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh | sh
```

Existing installs: press `F10` in the TUI or run `marshal update install`.
