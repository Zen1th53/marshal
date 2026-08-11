# ARTIFACT-PROVENANCE.md — Artifact Identity and Provenance Protocol

## Mission

Make every important artifact answer:

```text
what is it?
which source produced it?
how was it produced?
under which environment?
how was it verified?
```

## Required Fields

For release/security-sensitive artifacts:

- source commit,
- build command/workflow,
- environment or builder identity,
- SHA-256 or stronger digest,
- producing task,
- verification references.

## Immutability

Do not overwrite an artifact while retaining the same identity.

New bytes require:
- new digest,
- new provenance record,
- re-verification.

## Generated Reports

A report is evidence only for the exact input/commit/configuration it analyzed.

## Rebuild

For reproducibility-sensitive projects, compare independently built artifact digests where practical.

If not reproducible, document why.
