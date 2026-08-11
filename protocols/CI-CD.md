# CI-CD.md — Pipeline and Promotion Protocol

## Mission

Treat CI/CD as an evidence and deployment system, not a green badge.

## Pipeline Truth

Discover repository-native CI configuration before assuming checks.

Record:
- required checks,
- optional checks,
- environments,
- artifact flow,
- approval gates,
- promotion path.

## Reruns

A rerun does not erase the first failure.

If a check is flaky:

```text
record flake
→ investigate
→ do not rerun-until-green and call it clean
```

## Artifact Flow

Prefer:

```text
build once
→ verify artifact
→ promote same immutable artifact
```

over rebuilding different bytes for each environment.

## Verification Binding

CI evidence binds to:
- commit,
- workflow/config version,
- environment,
- artifact digest where applicable.

## Promotion

Production promotion requires:
- current required gates,
- valid approval if policy requires,
- known rollback,
- artifact identity,
- no stale verification.

## CI Trust Boundary

Untrusted pull-request code must not receive privileged secrets merely because CI can access them.

Apply AppSec supply-chain rules.
