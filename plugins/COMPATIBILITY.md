# Plugin Compatibility

## Versioning

Core pack:
- SemVer.

Runtime:
- SemVer specification.

External protocols:
- their native version scheme.

Plugin manifest must state compatibility explicitly.

## Failure

Optional plugin failure should degrade only its capability.

Examples:
- TurboVec down → semantic retrieval unavailable; canonical memory remains.
- telemetry exporter down → work may continue if audit policy allows.
- policy backend down → privileged operations fail closed.
