# Marshal v0.2.10

## Fixed

- **The Marshal really tells you when a run is done.** In 0.2.9 the "run
  closed" notice could stay at "sending" forever and never reach the chat, and
  messages you typed afterwards could get stuck behind it. It is now delivered
  once the chat is idle.
- **The final check takes about half as long.** A run the Marshal closes on its
  own now checks the finished work once instead of twice.
- **Secrets stay out of later commits too.** When Marshal starts a repository
  and leaves out files that look like secrets (such as `deploy.p12`), it also
  lists them in `.gitignore` in the same first commit, so a later `git add .`
  cannot commit them. `.p12` and `.pfx` certificates are in the default
  `.gitignore`.
- The line printed when Marshal starts a repository names both commits it
  makes, and the project scan no longer counts Marshal's own files, so a new,
  empty folder reads as an empty project.
- The Marshal says the architecture is ready and opens when you press `y`,
  instead of claiming it is already open in your browser, and the chat no
  longer shows a failed "project context is not ready yet" call before you
  accept the architecture.
- `marshal doctor` names a graphics card's vendor once and shows resource
  pressure (for example very low disk space) as a warning with its reason.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/Zen1th53/marshal/main/install.sh | sh
```

Existing installs: press `F10` in the TUI or run `marshal update install`.
