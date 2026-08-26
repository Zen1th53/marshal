# Adapter contracts

Adapters map provider bootstrap, process, permission, session, and evidence
surfaces into MARSHAL. They do not change task ownership, role authority, or
policy.

The v1.0.1 runtime resolves only `codex`, `opencode`, `gemini`, and `claude`.
Other entries in the compatibility matrix are contract research and are not
runtime support claims.

Capability labels in the contract files mean:

- `native`: the upstream tool exposes the surface;
- `emulated`: MARSHAL would have to provide it;
- `probe_required`: the installed version must be checked; and
- `unsupported`: no native capability is claimed.

Canonical contract sources:

- [matrix](../adapters/MATRIX.json)
- [contract](../adapters/CONTRACT.md)
- [compatibility notes](../adapters/COMPATIBILITY.md)
- [adapter-specific contracts](../adapters/README.md)
