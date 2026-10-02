# Problems and fixes

Start with `/help` inside MARSHAL, or `marshal doctor` in the terminal. Most
problems are explained there. If not, find your problem below.

## Installing and starting

??? question "`marshal: command not found`"
    The installer put MARSHAL in `~/.local/bin`, and your terminal does not
    look there yet. Add this line to `~/.bashrc` (or `~/.zshrc`), then open a
    new terminal:

    ```bash
    export PATH="$HOME/.local/bin:$PATH"
    ```

??? question "MARSHAL cannot open my project"
    MARSHAL needs a Git project. In your project folder, run:

    ```bash
    git status        # if this fails, run: git init
    marshal init
    marshal doctor
    ```

    `marshal doctor` tells you what is missing.

??? question "The installer says my system is not supported"
    Release builds are for Linux on 64-bit Intel/AMD or ARM. On other systems
    you can try building from source (see the end of
    [Install MARSHAL](install.md)), but this is not supported yet.

## Commands

??? question "I typed something and nothing happened"
    Text without a `/` at the start does not do anything. Every action is a
    command, such as `/marshal chat`. Type `/help` to see them.

??? question "`/marshal` only shows status"
    That is expected. `/marshal` on its own shows the current state. To start
    planning, use `/marshal chat`.

??? question "MARSHAL says \"Did you mean ...?\""
    You made a typo in a command. MARSHAL does nothing rather than guess.
    Type the suggested command.

??? question "Enter picked a suggestion instead of running my command"
    When a suggestion is highlighted, Enter accepts it first. Press Enter
    again to run the command.

## Agents

??? question "MARSHAL cannot find my agent, or it does not respond"
    1. Run the agent on its own (for example `codex`) and make sure you are
       signed in.
    2. Run `marshal doctor --probe-providers` to see what MARSHAL finds.

    Finding the agent does not prove you are signed in. Sign-in always
    happens in the agent's own interface.

??? question "MARSHAL asks which agent to use"
    You have more than one agent installed and have not chosen your main
    one. Choose it once for this project:

    ```text
    /provider use claude
    ```

??? question "A command for my agent is marked as not checked"
    Your agent's version is different from the one MARSHAL was tested with.
    The command may still work. See [Connect your AI agents](agents.md).

## Running work

??? question "The work does not start, or MARSHAL says it is blocked"
    Read the reason MARSHAL shows. Common causes:

    - Something is waiting for your approval. Check `/approvals`.
    - You changed the goal, so earlier approvals no longer apply.
    - A limit you set has been reached. Check `/budget`.
    - Bubblewrap is not installed. See [Install MARSHAL](install.md).

??? question "The budget shows UNKNOWN"
    Some agents do not report tokens or cost. MARSHAL shows `UNKNOWN` instead
    of guessing. Limits by number of calls and time still work.
    See [Pause, stop and set limits](control.md).

??? question "I closed the window during a run"
    Open MARSHAL again, check `/marshal status`, and use `/marshal resume`.

??? question "Ctrl+N does nothing"
    The navigation screens are switched off in this version. Use commands in
    the composer instead.

## Undoing things

??? question "An agent changed something I did not want"
    Go back to a checkpoint. `/checkpoint list` shows them, and
    `/rollback CHECKPOINT-ID` shows a preview before anything changes.
    See [Undo changes](undo.md).

??? question "Does rolling back also restore MARSHAL's records?"
    No. A rollback restores your project files. Restoring MARSHAL's records
    is a separate step: see [Restore a backup](undo.md#restore-a-backup).

## Other questions

??? question "Is what MARSHAL remembers from conversations reliable?"
    Treat it as notes, not facts. Check important details against your
    project.

??? question "Does `/update install` restart MARSHAL?"
    No. It downloads and checks the new version. Close MARSHAL and open it
    again to use it.
