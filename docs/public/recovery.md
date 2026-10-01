# Checkpoints, rollback and backups

A checkpoint preserves project files; a database backup preserves MARSHAL state.
Neither replaces your Git history or a separate backup strategy.

## Project files

```text
/checkpoint create Before changing the build configuration
/checkpoint list
/checkpoint inspect CHECKPOINT-ID
/checkpoint diff OLD-ID NEW-ID
/rollback CHECKPOINT-ID
```

Use identifiers from the listing. Checkpoints exclude `.git` and `.marshal`.
Inspection verifies the stored files; altered or missing snapshots cannot be
restored. Diff lists at most 200 changed paths. Collaboration handoff records
are separate and have no restorable project files.

Rollback first previews files to restore, delete and overwrite. To proceed,
copy its confirmation command, `/rollback <id> confirm <digest>`. No run may
be active. MARSHAL captures a recovery checkpoint first and verifies restored
files afterwards. Use the reported recovery point if you need to undo a restore.

## MARSHAL state

```text
/backup create
/backup restore ./marshal-backup.db
```

Creation reports the verified backup's actual location. Substitute that location
in restore; the filename above is illustrative. Restore first verifies and
previews. On Linux, the displayed `/backup restore <path> confirm <digest>`
coordinates a live restore, backs up current state, and refuses if another
MARSHAL window or daemon holds the database. Elsewhere use offline restore.

For the offline path, exit all MARSHAL windows and stop the daemon first:

```bash
marshal state verify-backup ./marshal-backup.db
marshal state restore ./marshal-backup.db
```

A separate CLI backup can be created with:

```bash
marshal state backup --output ./marshal-backup.db
```

Database restore replaces project state; it does not roll back project source
files. Keep backups private: they can contain your project conversations.
