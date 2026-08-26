# Web Control Plane API map

The authoritative v1.0.1 route classification is the
[API reality matrix](API-REALITY-MATRIX.md).

Canonical live integrations are intentionally narrow:

- runtime/build health through `/api/v1/system/status`;
- bounded host visibility through `/api/v1/resources`;
- diagnostic health through `/api/v1/health/doctor`;
- scope-authorized memory recall, record detail, working slots, and governed
  mutations under `/api/v1/memory/*`; and
- real backup list/create/verify operations under
  `/api/v1/operations/backups*`.

All other registered handlers are blocked with `501` when a live runtime is
attached until they have a real canonical backing path. This is an explicit
fail-closed product boundary, not a roadmap claim.
