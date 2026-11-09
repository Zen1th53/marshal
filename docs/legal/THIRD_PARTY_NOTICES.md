# Third-Party Notices & Attribution

This document contains licensing and copyright notices for third-party software, specifications, standards, and components incorporated in or utilized by the MARSHAL project.

---

## 1. In-Tree Third-Party Material

### Contributor Covenant (Code of Conduct)
* **File**: `.github/CODE_OF_CONDUCT.md`
* **Upstream**: Contributor Covenant v3.0 (Organization for Ethical Source)
* **License**: Creative Commons Attribution-ShareAlike 4.0 International (`CC BY-SA 4.0`)
* **Notice**: Adaptations and verbatim copies of Contributor Covenant are licensed under CC BY-SA 4.0. See [`https://creativecommons.org/licenses/by-sa/4.0/`](https://creativecommons.org/licenses/by-sa/4.0/).

---

## 2. Go Module Dependencies

All external Go module dependencies are managed via `go.mod` and fetched during build time. One first-party module is vendored (below); no third-party dependency source code is vendored into the repository tree.

**First-party vendored module.** `github.com/Zen1th53/code-evidence` (Code
Evidence Context) is the owner's own module, vendored at
`third_party/code-evidence` from upstream commit `75e9c3d` and built through a
`replace` directive in `go.mod`, so release builds need no network fetch. See
`third_party/code-evidence/UPSTREAM`. Its license is not yet chosen upstream
(owner decision); until it is, it ships only as part of Marshal under
MARSHAL's own terms and the dependency audit reports it as review-required.

| Dependency Module | Version | License | Upstream Copyright / Notice |
|---|---|---|---|
| `go.yaml.in/yaml/v3` | `v3.0.5` | MIT / Apache-2.0 | Copyright (c) 2011-2019 Canonical Ltd |
| `modernc.org/sqlite` | `v1.57.0` | BSD-3-Clause | Copyright (c) 2017 The C-Go Authors |
| `github.com/dustin/go-humanize` | `v1.0.1` | MIT | Copyright (c) 2010 Dustin Sallings |
| `github.com/google/uuid` | `v1.6.0` | BSD-3-Clause | Copyright (c) 2009, 2014 Google Inc. |
| `github.com/mattn/go-isatty` | `v0.0.24` | MIT | Copyright (c) Yasuhiro Matsumoto |
| `github.com/ncruces/go-strftime` | `v1.0.0` | MIT | Copyright (c) Nuno Cruces |
| `github.com/remyoudompheng/bigfft` | `v0.0.0...` | BSD-3-Clause | Copyright (c) 2012 Rémy Oudompheng |
| `golang.org/x/sys` | `v0.47.0` | BSD-3-Clause | Copyright (c) 2009 The Go Authors |
| `modernc.org/libc` | `v1.74.4` | BSD-3-Clause | Copyright (c) 2017 The C-Go Authors |
| `modernc.org/mathutil` | `v1.7.1` | BSD-3-Clause | Copyright (c) 2017 The C-Go Authors |
| `modernc.org/memory` | `v1.11.0` | BSD-3-Clause | Copyright (c) 2017 The C-Go Authors |

---

## 3. External Runtime Tools

Marshal runs separately installed tools: `git`, `tmux` and the provider CLIs
you choose. None of them is linked into Marshal, vendored in this repository,
or bundled in release archives.

Provider CLIs are likewise discovered and executed as separately installed
operator-selected tools; MARSHAL does not redistribute them.

---

## 4. Policy Regarding Future Third-Party Submissions

Contributors must explicitly disclose any third-party code, snippets, dependencies, or materials included in Pull Requests. Copyright assignment agreements do **not** assign third-party copyrights to MARSHAL. See [`docs/legal/THIRD-PARTY-POLICY.md`](THIRD-PARTY-POLICY.md).
