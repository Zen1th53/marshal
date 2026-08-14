# Contributor Model Decision Document

This document records the architectural rationale and comparison of contributor-rights models considered for MARSHAL, leading to the selection of a **Copyright Assignment + Patent Grant** model.

---

## 1. Evaluation of Contributor Models

| Model | Primary Mechanism | Copyright Ownership | Dual-Licensing Control | Acquisition / M&A Due Diligence |
|---|---|---|---|---|
| **Inbound = Outbound** (Default GitHub) | Implicit license grant under repo license | Fragmented among all contributors | Extremely difficult (requires 100% contributor consent) | High risk (fragmented IP title) |
| **Developer Certificate of Origin (DCO)** | Developer declaration of right to submit | Fragmented among all contributors | Extremely difficult | High risk |
| **Apache CLA** (ICLA / CCLA) | Broad non-exclusive license grant | Retained by contributor | Partial (depends on terms, but IP title remains fragmented) | Moderate (requires verifying license grants, not title) |
| **Harmony Agreement** (Assignment Option Ah) | Full copyright assignment | Centralized with project owner | Complete (unrestricted dual-licensing rights) | Low risk (clean chain-of-title) |
| **FSF Copyright Assignment** | Full copyright assignment | Centralized with FSF | Complete for FSF stewardship | Low risk |

---

## 2. Key Distinction: License Grant vs. Copyright Assignment

### Apache CLA (License Grant)
Under an Apache-style CLA (such as the Apache ICLA/CCLA), contributors retain their individual copyright ownership and grant the foundation a broad, non-exclusive license to reproduce and distribute their work. While this authorizes open-source distribution, **it leaves underlying copyright ownership fragmented across hundreds of individual authors**.

If a project owner later seeks to change the open-source license model, offer custom proprietary commercial licenses, or sell/transfer project IP in an acquisition, fragmented copyright ownership creates substantial legal friction and due-diligence risk.

### Harmony / FSF Model (Copyright Assignment) — Chosen by MARSHAL
Under a Copyright Assignment model (inspired by Harmony Agreement Assignment option `Ah` and FSF copyright assignment principles), contributors assign their applicable copyright title in accepted contributions to the project owner while retaining authorship credit in Git history and receiving broad patent grants.

---

## 3. Rationale for MARSHAL Selection

MARSHAL selected **Copyright Assignment + Patent Grant** (ICAA / CCAA) for the following strategic reasons:

1. **Clean Chain-of-Title**: Centralizes project-owned copyright ownership with the project owner, eliminating IP fragmentation.
2. **Dual-Licensing Flexibility**: Enables the owner to offer both an `AGPL-3.0-only` community edition and custom Commercial Licenses to corporate users without requiring individual consent from every past contributor.
3. **Acquisition Readiness**: Allows an acquiring company or corporate successor to purchase or receive project IP without needing to re-contact or obtain fresh signatures from hundreds of past contributors.
4. **Corporate Contributor Protection**: CCAA explicitly handles employer work-for-hire rights, ensuring corporate legal teams can safely authorize employee contributions.
5. **Patent Security**: Includes explicit, reciprocal patent grants protecting the project and downstream users from contributor patent claims.
