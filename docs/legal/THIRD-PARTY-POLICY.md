# Third-Party Code Contribution Policy

This policy governs the inclusion of third-party source code, libraries, schemas, test data, or external material in contributions to the MARSHAL project.

---

## 1. Core Rules

1. **No Falsified Assignment**: The Contributor Assignment Agreement (ICAA/CCAA) assigns rights in original code created by the contributor. It does **NOT** and cannot assign third-party copyrights to MARSHAL.
2. **Explicit Disclosure**: Any third-party code, library, or snippet included in a Pull Request must be explicitly declared in the PR description prior to review.
3. **Preservation of Notices**: All original copyright headers, license notices, and SPDX identifiers in third-party files must be preserved intact.
4. **License Compatibility**: Third-party material must be licensed under a permissive open-source license compatible with both AGPL-3.0-only distribution and commercial dual-licensing (e.g., MIT, BSD-2/3-Clause, Apache-2.0).
5. **No Incompatible Copyleft**: Contributions containing GPL, AGPL, LGPL, or MPL code from third parties will **not** be incorporated directly into the MARSHAL core repository tree.

---

## 2. Distinction of Submission Categories

```text
                               Submitted PR
                                    │
           ┌────────────────────────┴────────────────────────┐
           │                                                 │
Original Contributor Code                          Third-Party Material
           │                                                 │
    Subject to ICAA/CCAA                              Retains Original
    Copyright Assignment                              License & Attribution
```

---

## 3. Disallowing Unauthorized Submissions

Contributors are strictly prohibited from submitting:

* Proprietary or trade-secret code belonging to a current or former employer without corporate authorization;
* Code under NDA or confidentiality obligations;
* Incompatibly-licensed copyleft source code disguised as original code.
