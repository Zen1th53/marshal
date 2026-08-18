# Upgrades and database migration

Back up the repository's `.marshal/` directory before changing MARSHAL
versions. Stop the daemon first so the SQLite database has no active writer.

1. Record `marshal version` and the current Git commit.
2. Stop the daemon and copy `.marshal/` to protected backup storage.
3. Install the new binary and verify its checksum and provenance.
4. Run `marshal doctor` in the repository.
5. Start MARSHAL and inspect `marshal status` and `marshal events`.

The current database schema is version 67. Schema versions are internal storage
contracts, not product release numbers. MARSHAL applies its supported forward
migration chain transactionally and rejects databases created by an unknown
future schema. Do not edit `schema_version` manually.

Rollback is safe only when the older binary understands the migrated schema.
When that is not documented, restore the pre-upgrade `.marshal/` backup instead
of opening the new database with an older binary.
