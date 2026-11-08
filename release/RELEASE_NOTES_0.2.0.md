# Marshal v0.2.0

Marshal now builds its unrestricted engine by default and ships as `marshal`.
Workers run on the host without a sandbox; a first-run notice explains access,
and `/` and `$HOME` itself are refused as project roots.

State lives in `.marshal/`. With old sessions closed, a lone `.marshal-extreme/`
is renamed atomically, stored paths are relocated and originals are retained.
Existing old public schema-90 state upgrades in place. If both directories
exist, neither is deleted or merged. Back up first and read
[upgrade and recovery instructions](../docs/upgrading.md).

Anonymous, opt-out telemetry is on at https://marshal.blackhat.uz. Disable it
with `marshal telemetry disable` or `MARSHAL_CLOUD_ENDPOINT=off`. Project content
is never sent; execution remains locally authorized and usable offline.

Linux amd64/arm64 archives, SHA-256 checksums, release manifests and the existing
installer/update feed continue in `Zen1th53/marshal`. No build tag is required.
