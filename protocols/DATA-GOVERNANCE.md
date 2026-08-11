# DATA-GOVERNANCE.md — Memory, Logging, and External Data Handling Protocol

## Mission

Prevent useful agent systems from becoming uncontrolled data-exfiltration or retention systems.

## Before Storing

Classify data using `memory/DATA-POLICY.md`.

Ask:
- is it needed?
- where will it live?
- who can read it?
- will it be embedded/indexed?
- will it leave the machine/org?
- when can it be deleted?

## Secrets

Never place secrets in:
- general memory,
- vector indexes,
- graph memory,
- RUNS logs,
- reusable prompts,
- artifacts intended for broad sharing.

## External Services

Before sending source/data externally:
- capability policy must allow,
- data policy must allow,
- repository/owner policy must allow,
- approval must exist when required.

## Redaction

Prefer removing sensitive fields before indexing/logging rather than relying only on access control afterward.

## Deletion

When a record must be deleted, consider replicas/indexes/backups.

Deletion from one Markdown file may not delete semantic/vector copies.
