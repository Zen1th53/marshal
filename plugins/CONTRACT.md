# Plugin Contract

A plugin declares:

```text
id
version
kind
marshal_version_range
runtime_spec_version_range
capabilities
required_permissions
data_classes
network_requirements
protocol_versions
failure_mode
```

## Core Rule

A plugin may add capability.

It may not silently:
- broaden role authority,
- bypass approval,
- override instruction trust,
- write another role's verdict,
- export data beyond policy.

## Lifecycle

```text
discover
→ validate manifest
→ compatibility check
→ security/capability review
→ enable
→ health/probe
→ disable/remove
```

Unknown plugin versions default to disabled.
