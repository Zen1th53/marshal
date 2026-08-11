# Agent OS Conformance

This directory tests whether an adapter/agent behaves according to the shared
Agent OS contract.

Two levels:

## Static Conformance

Executed by `conformance/runner.py`.

Checks:
- pack references,
- core adapter presence,
- scenario/fixture integrity,
- binary discovery.

## Behavioral Conformance

An adapter/runtime runs a scenario against a real installed agent and records:

```text
prompt/input
agent version
adapter version
repository HEAD
tool permissions
events/actions
final verdict
```

Behavioral conformance is not simulated by the static runner.

## Commands

```bash
python conformance/runner.py validate-pack
python conformance/runner.py probe-adapters
python -m unittest discover -s conformance/tests -v
```
