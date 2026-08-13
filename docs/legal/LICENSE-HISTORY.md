# SLAVES — License History

## Overview

This document records the licensing history of the SLAVES project to maintain
a clear, auditable record of licensing changes over time.

---

## Historical Licensing: Apache-2.0

All SLAVES releases prior to and including the licensing migration commit
documented below were distributed under the Apache License, Version 2.0.

### Releases Distributed Under Apache-2.0

| Tag | Date | License |
|---|---|---|
| `runtime-v0.2.1` | Pre-migration | Apache-2.0 |
| `runtime-v0.3.0` | Pre-migration | Apache-2.0 |
| `runtime-v0.3.1` | Pre-migration | Apache-2.0 |
| `runtime-v0.4.0` | Pre-migration | Apache-2.0 |

### Apache-2.0 Grant Preservation

Recipients of those historical versions received rights under the Apache
License 2.0. **Those grants remain associated with the distributed versions
and are not revoked.** Any person who obtained a copy of SLAVES under
Apache-2.0 retains the rights granted to them by that license for the
version they received.

The official Apache License 2.0 text is preserved at:
[`LICENSES/Apache-2.0.txt`](../../LICENSES/Apache-2.0.txt)

---

## Current Licensing: AGPL-3.0-only (Community) + Commercial

Beginning with the licensing migration commit identified below, SLAVES
uses a dual-licensing model:

### Community License

The SLAVES community edition is licensed under the GNU Affero General
Public License, version 3.0 only (`AGPL-3.0-only`).

The full license text is in the root [`LICENSE`](../../LICENSE) file.

### Commercial License

Organizations requiring alternative licensing terms may obtain a separate
commercial license from the copyright owner. See
[`LICENSING.md`](../../LICENSING.md) for details.

---

## Migration Commit

The licensing migration from Apache-2.0 to AGPL-3.0-only was performed in
the following commit:

```
Branch: chore/acquisition-ready-licensing
Commit: [TO BE RECORDED AFTER MERGE]
```

This commit:

1. Replaced the root `LICENSE` file with the AGPL-3.0-only text.
2. Preserved the Apache-2.0 text at `LICENSES/Apache-2.0.txt`.
3. Updated all project references to reflect the new licensing model.
4. Did NOT rewrite Git history or delete historical tags.
5. Did NOT revoke or purport to revoke Apache-2.0 grants already made
   to recipients of prior versions.

---

## Important Clarifications

- **Historical Apache-2.0 copies**: Anyone who received a copy of SLAVES
  under Apache-2.0 retains the rights granted by that license for the
  version they received.

- **New versions**: Versions released after the migration commit are
  available under AGPL-3.0-only (community) or under a separate
  commercial license from the copyright owner.

- **No retroactive relicensing of distributed copies**: The migration
  changes the license for new releases going forward. It does not and
  cannot retroactively change the license terms under which prior copies
  were already distributed.
