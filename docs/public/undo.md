# Undo changes and keep backups

MARSHAL gives you two safety nets:

- **Checkpoints** save your project files, so you can go back to them.
- **Backups** save MARSHAL's own records: your goals, plans and history.

Neither replaces Git. Keep committing your work as usual.

## Save a checkpoint

Before a risky change, save a snapshot with a short reason:

```text
/checkpoint create Before changing the build settings
```

MARSHAL also saves a checkpoint by itself before `/apply` and before a
rollback.

To see your checkpoints:

```text
/checkpoint list
```

## Go back to a checkpoint

Use the checkpoint ID from the list:

```text
/rollback CHECKPOINT-ID
```

This does not change anything yet. MARSHAL first shows which files would be
restored, changed or deleted, and gives you a confirmation command. If the
preview looks right, copy that command and run it. It looks like this:

```text
/rollback CHECKPOINT-ID confirm DIGEST
```

Before restoring, MARSHAL saves a checkpoint of your current files, so you can
undo the rollback too. You cannot roll back while a run is in progress.

!!! note
    Checkpoints do not include the `.git` folder or MARSHAL's own `.marshal`
    folder.

## Compare two checkpoints

```text
/checkpoint diff OLD-ID NEW-ID
```

## Back up MARSHAL's records

```text
/backup create
```

MARSHAL tells you where it saved the backup.

!!! warning "Backups are private"
    A backup can contain your conversations with the agents. Do not share it.

## Restore a backup

Restoring replaces MARSHAL's records with the backup. It does not change your
project files.

On Linux, you can restore from inside MARSHAL. Close any other MARSHAL windows
first, then run:

```text
/backup restore PATH-TO-BACKUP
```

Like rollback, this first shows a preview and a confirmation command. MARSHAL
backs up your current records before restoring.

You can also restore from the terminal. Close all MARSHAL windows and stop
`marshal daemon` if you started it, then run:

```bash
marshal state verify-backup PATH-TO-BACKUP
marshal state restore PATH-TO-BACKUP
```
