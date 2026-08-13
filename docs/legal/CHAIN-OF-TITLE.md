# SLAVES Chain-of-Title & IP Acquisition Due-Diligence Record

This document provides a comprehensive, auditable record of intellectual property ownership, copyright provenance, contributor agreements, licensing history, and chain-of-title documentation for the SLAVES project (`https://github.com/Zen1th53/slaves`).

---

## 1. Executive Summary & IP Ownership Architecture

```text
                                SLAVES IP
                                    │
                        Current Copyright Owner
                                    │
          ┌─────────────────────────┼─────────────────────────┐
          │                         │                         │
  Founder Code           External Contributions      Historical Apache
 (100% Owned)            (ICAA / CCAA Assigned)      (v0.2.0 - v0.4.0)
          │                         │                         │
          └─────────────────────────┼─────────────────────────┘
                                    │
                          Centralized Ownership
                                    │
             ┌──────────────────────┴──────────────────────┐
             │                                             │
      AGPL-3.0-only                                  Commercial
   Community License                              Dual-Licensing
             │                                             │
             └──────────────────────┬──────────────────────┘
                                    │
                       Clean Chain of Title
                                    │
                       Future Acquisition / Sale
```

---

## 2. Chain-of-Title Breakdown

### A. Founder & Owner-Created Code
- **Status**: 100% Owner-Created.
- **Evidence**: Complete Git commit history from repo inception to commit `23719e0` (70 commits) is authored exclusively by `Zen1th53 <extreme29@proton.me>`.
- **Ownership**: Centralized with the founder/owner.

### B. External Individual Contributions
- **Framework**: Individual Contributor Assignment Agreement (ICAA) ([`legal/INDIVIDUAL-CONTRIBUTOR-ASSIGNMENT.md`](../../legal/INDIVIDUAL-CONTRIBUTOR-ASSIGNMENT.md)).
- **Registry**: [`legal/assignment-registry.yml`](../../legal/assignment-registry.yml).
- **CI Gate**: [`.github/workflows/contributor-rights-check.yml`](../../.github/workflows/contributor-rights-check.yml).
- **Transferability**: All accepted individual contributions assign copyright to the project owner without requiring fresh consent for future corporate acquisition or license changes.

### C. Corporate & Employer-Owned Contributions
- **Framework**: Corporate Contributor Assignment Agreement (CCAA) ([`legal/CORPORATE-CONTRIBUTOR-ASSIGNMENT.md`](../../legal/CORPORATE-CONTRIBUTOR-ASSIGNMENT.md)).
- **Authority**: Authorized corporate officer execution; covers employee work-for-hire IP assignments.

### D. Patent Grants
- **Scope**: Both ICAA and CCAA include perpetual, worldwide, royalty-free patent grants covering patent claims necessarily infringed by accepted contributions.

### E. Third-Party Code & Dependencies
- **In-Tree**: Contributor Covenant (`CODE_OF_CONDUCT.md`) under `CC BY-SA 4.0`.
- **Dependencies**: All Go module dependencies are managed externally (`go.mod`) and licensed under permissive open-source licenses (MIT, BSD-3-Clause, Apache-2.0). See [`THIRD_PARTY_NOTICES.md`](../../THIRD_PARTY_NOTICES.md).

### F. Historical Releases (Apache-2.0)
- **Releases**: `runtime-v0.2.1`, `runtime-v0.3.0`, `runtime-v0.3.1`, `runtime-v0.4.0`.
- **Preservation**: Historical Apache-2.0 license grants remain associated with those distributed versions. See [`docs/legal/LICENSE-HISTORY.md`](LICENSE-HISTORY.md).

### G. Current & Future Releases (AGPL-3.0-only + Commercial)
- **Community**: `AGPL-3.0-only`.
- **Commercial**: Dual-licensing rights centralized with project owner.

---

## 3. Purchaser Due-Diligence Checklist

For legal, M&A, and technical due-diligence teams assessing SLAVES for corporate acquisition, strategic investment, or asset transfer:

| Question | Answer | Evidence / Location |
|---|---|---|
| **Who owns the core source code?** | Solely owned by founder / project owner. | 100% single-author Git commit log; [`docs/legal/IP-PROVENANCE-AUDIT.md`](IP-PROVENANCE-AUDIT.md) |
| **Are contributor copyrights centralized?** | Yes, all material external contributions require full copyright assignment. | [`legal/INDIVIDUAL-CONTRIBUTOR-ASSIGNMENT.md`](../../legal/INDIVIDUAL-CONTRIBUTOR-ASSIGNMENT.md) |
| **Are assignment records available?** | Yes, tracked in machine-readable registry and private legal archives. | [`legal/assignment-registry.yml`](../../legal/assignment-registry.yml) |
| **Are corporate contributors covered?** | Yes, via corporate agreement (CCAA) signed by authorized corporate officers. | [`legal/CORPORATE-CONTRIBUTOR-ASSIGNMENT.md`](../../legal/CORPORATE-CONTRIBUTOR-ASSIGNMENT.md) |
| **Are patent grants documented?** | Yes, patent grants are integrated into ICAA and CCAA agreements. | ICAA Section 4; CCAA Section 4 |
| **Which third-party licenses exist?** | Permissive only (MIT, BSD-3-Clause, CC BY-SA 4.0 for CoC). | [`THIRD_PARTY_NOTICES.md`](../../THIRD_PARTY_NOTICES.md) |
| **Which historical versions were Apache?** | `runtime-v0.2.1` through `runtime-v0.4.0`. Historical grants remain valid for those versions. | [`docs/legal/LICENSE-HISTORY.md`](LICENSE-HISTORY.md) |
| **Which versions are AGPL?** | Versions released after the migration commit (`chore/acquisition-ready-licensing`). | Root [`LICENSE`](../../LICENSE) |
| **Who controls commercial licensing?** | Solely controlled by the SLAVES project copyright owner. | [`LICENSING.md`](../../LICENSING.md) |
| **Are trademarks/domains controlled separately?** | Domain and GitHub repository are controlled by project owner (`Zen1th53`). | [`docs/legal/OWNER-AND-SUCCESSOR-MODEL.md`](OWNER-AND-SUCCESSOR-MODEL.md) |
| **Are there outstanding IP disputes?** | None. Zero infringement claims, zero third-party notices, zero disputes. | Verified audit state |
| **Are any contributions missing agreements?** | No. Current codebase has 0 external contributors. Future PRs are blocked by CI gate. | [`.github/workflows/contributor-rights-check.yml`](../../.github/workflows/contributor-rights-check.yml) |
