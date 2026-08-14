# Conformance

MARSHAL currently defines 26 adversarial and fault scenarios in
[conformance/SCENARIOS.json](../conformance/SCENARIOS.json). The repository
separates three kinds of evidence.

## Static conformance

Checks pack references, adapter presence, scenario fixtures, and optional
binary discovery:

```bash
python conformance/runner.py validate-pack
python conformance/runner.py probe-adapters
```

## Behavioral conformance

Runs an adapter command and records observable behavior, environment, version,
and repository state. A static fixture is not a behavioral PASS.

```bash
python conformance/behavioral_runner.py --expected DENY -- adapter-command
```

## Executable Runtime V1 conformance

Selected scenarios map to real Go tests for leases, worktrees, policy,
verification invalidation, reconciliation, and worker failure:

```bash
python conformance/runtime_runner.py --scenario CONF-003
```

Unmapped scenarios report `NOT_RUN`; they are never promoted to PASS.

## Verdicts

- `PASS`: observed behavior satisfies the invariant.
- `FAIL`: observed behavior violates it.
- `BLOCKED`: a required capability, credential, or environment is unavailable.
- `SKIP`: the scenario does not apply to an explicitly unsupported capability.

See [conformance/CONTRACT.md](../conformance/CONTRACT.md) and
[conformance/ADVERSARIAL.md](../conformance/ADVERSARIAL.md).
