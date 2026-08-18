# MARSHAL examples

The examples in this directory use public, synthetic data and are intended to
be run from the repository root.

## Policy conformance

`policy-test-suite.json` is a complete declarative policy test. It embeds a
deny-by-default policy, binds the test to that policy's canonical SHA-256
digest, and expects the denial. It does not contact a provider or execute the
resource named in the fixture.

```bash
marshal policy test examples/policy-test-suite.json
```

The expected result is `PASS`. A passing test is evidence about policy behavior;
it is not runtime authorization.

Provider-backed task examples are documented in
[Getting started](../docs/getting-started.md). They require the selected
provider binary and the operator's own authentication.
