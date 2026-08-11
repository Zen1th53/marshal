# Reproducible Build / Packaging Protocol

## Goal

Same declared source/input should produce semantically identical release content.

## ZIP Determinism

A production packager should normalize:
- file ordering,
- timestamp metadata,
- permissions,
- compression settings,
- generated manifest ordering.

## Generated Files

Generated manifest/provenance must state the inputs used.

## Verification

At minimum:
- validate pack,
- run tests,
- generate manifest,
- independently re-hash,
- compare file inventory.

For stronger reproducibility:
- perform two independent builds and compare normalized artifact digest.

If exact reproducibility is not achieved, state the reason instead of claiming it.
