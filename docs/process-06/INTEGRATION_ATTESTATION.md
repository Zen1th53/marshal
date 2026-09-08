# Process 06 integration attestation

This record binds the qualified Process 06 candidate to the exact canonical
`main` that resulted from merging it. A candidate qualification is not
integrated product truth; this attestation is what closes rows AA and AP.

## Binding

| Field | Value |
|---|---|
| Candidate SHA | `9886f5787a8226ab607758a3ef5c6e97c6432bab` |
| Resulting main SHA | `a53b4a24747f8c1cf02ea4ab99d9207cb39effe7` |
| Merge tree digest | `8bd1ff29fd88f24cbe897b1573fc324b090bba6c` |
| Merge commit | PR #111, ordinary merge, branch deleted |
| Ancestor proof | `git merge-base --is-ancestor` returns true |
| Base before merge | `0238e7e4ba308767d7fd14468e995022c6114a37` |
| Process 05 dependency | `e56e7af9ec13906ae9c6a83d288819e9642f37ca`, already in main |
| Schema | v83 |
| Author and committer | Zen1th53 on every commit |
| AI attribution | none in messages, trailers, branches or tags |

## Exact-main critical gates

Re-run against the checked-out `a53b4a2` tree, not against the candidate
branch.

| Gate | Status |
|---|---|
| `go build ./...` | PASS |
| `go vet ./...` | PASS |
| `go test ./...` | PASS, 133 packages, 0 failures |
| `TestProcess05_FullChainE2E` and failure chain | PASS |
| Process 06 fuzz, 10 seconds | PASS |
| `distribution/PACK-MANIFEST.json` | PASS |
| `conformance/runner.py validate-pack` | PASS |
| `internal/verification` present in main | PASS, 10 files |
| Internal packs absent from main | PASS, 0 `MARSHAL_*` files |

## GitHub checks

Recorded from PR #111 on head `9886f57`, all six green:

- Analyze Go Code
- CodeQL
- Runtime & Pack Verification (the required check; runs `go test -race` and the Python suites)
- Verify Contributor IP Assignment
- Web Control Plane Frontend
- dependency-review

PR-only versus push-to-main distinction: every check above ran on the pull
request. `Runtime & Pack Verification` is the only required check configured
on the protected branch. No check is push-to-main exclusive, so no gate was
skipped by merging.

## Known gaps

Recorded, not upgraded.

- Row P, real governed provider verification: `NOT_RUN`. No external provider
  credentials were used. Provider configuration enforcement stays covered by
  repository integration tests. Treating configuration tests as proof of real
  provider execution would be the assertion-as-evidence failure this process
  exists to prevent.
- `gosec` reports pre-existing findings elsewhere in the repository. None are
  in the new verification code. Process 06 does not patch implementation it is
  meant to judge.
- The removed internal evidence files remain reachable in earlier commits.
  Removal is forward-only; history was deliberately left intact.

## Historical tags

Nine tags, `v1.0.0` through `v1.5.0`, are unchanged. This was not a release
task and no tag was created, moved or deleted.

## Status

`PROCESS 06 VERIFIED AND INTEGRATED`

Bounded to the exact state above. Changing the Goal, Plan, Run, tree or
evidence invalidates the applicability of this attestation. It is a digested
statement about one bounded state, not proof for all time.
