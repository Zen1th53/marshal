# Third-Party Notices & Attribution

This document contains licensing and copyright notices for third-party software, specifications, standards, and components incorporated in or utilized by the SLAVES project.

---

## 1. In-Tree Third-Party Material

### Contributor Covenant (Code of Conduct)
* **File**: `CODE_OF_CONDUCT.md`
* **Upstream**: Contributor Covenant v3.0 (Organization for Ethical Source)
* **License**: Creative Commons Attribution-ShareAlike 4.0 International (`CC BY-SA 4.0`)
* **Notice**: Adaptations and verbatim copies of Contributor Covenant are licensed under CC BY-SA 4.0. See [`https://creativecommons.org/licenses/by-sa/4.0/`](https://creativecommons.org/licenses/by-sa/4.0/).

---

## 2. Direct Go Module Dependencies

All external Go module dependencies are managed via `go.mod` and fetched during build time. No dependency source code is vendored into the repository tree.

| Dependency Module | Version | License | Upstream Copyright / Notice |
|---|---|---|---|
| `go.yaml.in/yaml/v3` | `v3.0.5` | MIT / Apache-2.0 | Copyright (c) 2011-2019 Canonical Ltd |
| `modernc.org/sqlite` | `v1.56.0` | BSD-3-Clause | Copyright (c) 2017 The C-Go Authors |
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

## 3. Policy Regarding Future Third-Party Submissions

Contributors must explicitly disclose any third-party code, snippets, dependencies, or materials included in Pull Requests. Copyright assignment agreements do **not** assign third-party copyrights to SLAVES. See [`docs/legal/THIRD-PARTY-POLICY.md`](docs/legal/THIRD-PARTY-POLICY.md).
