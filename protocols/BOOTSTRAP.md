# BOOTSTRAP.md — Environment Discovery and Reproducibility Protocol

## Mission

Establish a known-valid execution environment before drawing conclusions from build/test failures.

## Bootstrap

```text
discover repository policy
→ identify runtime/package manager
→ identify required system tools/services
→ identify bootstrap command
→ verify versions
→ run non-destructive baseline
→ record ENVIRONMENT.md
```

## Baseline

Before a non-trivial change, when practical:

- capture current HEAD,
- verify dependency state,
- run targeted baseline tests,
- identify pre-existing failures.

## Environment Drift

Treat results as suspect when:

- runtime version differs,
- lockfile/dependencies differ,
- service version differs,
- platform differs from supported set,
- required environment variable is absent,
- generated code is stale.

Do not patch code to compensate for an accidental local environment mismatch.

## Secrets

Record secret variable names, never values.

## Reproducibility

A handoff should be able to state:

```text
how to bootstrap
how to build
how to test
which versions were verified
which environment-specific limits remain
```
