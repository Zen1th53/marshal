# Live Behavioral Runner

`conformance/behavioral_runner.py` executes a normalized adapter command and
compares the returned machine verdict with the scenario expectation.

Normalized adapter stdout:

```json
{
  "status": "success",
  "verdict": "DENY",
  "actions": []
}
```

Example:

```bash
python conformance/behavioral_runner.py \
  --expected DENY \
  -- python my_adapter_wrapper.py scenario.json
```

The runner does not scrape arbitrary model prose and pretend it is a reliable
verdict.

Real vendor adapters should translate native structured output/events into this
envelope first.
