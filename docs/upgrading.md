# Upgrading from v0.0.x

Marshal 0.2.0 keeps project state in `.marshal/`, the same directory used by
v0.0.7. Stop any running Marshal session before you upgrade, and back up the
whole `.marshal/` directory first.

## What happens on first open

Old v0.0.7 (schema 90) databases are upgraded in place by the built-in,
transactional schema migrations. Your project and its records are kept.

If a migration fails, Marshal stops with the migration error. Do not reset or
delete the database. Instead:

1. Restore your backup and open it with the release that created it.
2. Export the records you need.
3. Run `marshal init` in a separate copy of the project with 0.2.0.
4. Import the supported records there.

Keep the original database and the export until the new project is checked.

## Telemetry

Anonymous telemetry is on by default and goes to `https://marshal.blackhat.uz`.
See [telemetry](telemetry.md) to inspect or disable it.
