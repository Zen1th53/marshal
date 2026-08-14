# Machine-Readable Schemas

MARSHAL V6 uses JSON Schema Draft 2020-12 for the portable control-plane records
that cross process, adapter, or host boundaries.

Schemas included:

- task
- event
- memory record
- finding
- approval
- artifact
- conformance result
- agent capability
- protocol negotiation
- telemetry event
- tenant context
- release manifest
- protocol extension

Validate an instance with:

```bash
python tools/schema_validate.py schemas/task.schema.json task.json
```

The Python helper uses the `jsonschema` package when available and a deliberately
limited fallback validator otherwise.

Schemas are contracts. Markdown remains the human-readable explanation.

Sample instances live under `schemas/samples/` and are validated during final pack verification.
