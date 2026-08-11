# TELEMETRY.md — Runtime Telemetry Protocol

## Mission

Make runtime behavior measurable without turning observability into a data leak.

## Required Correlation

Where available:
- tenant/project,
- agent/session,
- task,
- repository commit,
- artifact,
- approval,
- conformance scenario.

## Error Semantics

Record failures and denials honestly.

A retry that later succeeds must not erase the original failure event.

## Chain of Thought

Never emit hidden reasoning.

Record concise externally useful action/result summaries only.
