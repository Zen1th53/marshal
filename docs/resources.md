# Community Resource Awareness

MARSHAL Community can inspect the local machine and make conservative safety recommendations. It is read-only and advisory: it does not alter sandbox policy, provider/network policy, task isolation, or scheduler concurrency.

## What is collected

- CPU model, logical/effective CPU count, and architecture;
- RAM, available RAM, swap use, and cgroup v2 CPU/memory limits where exposed;
- free space for the MARSHAL state location;
- generic Linux accelerator inventory through `/sys/class/drm`, with best-effort temperature and dedicated-memory telemetry from `/sys/class/hwmon` and DRM memory attributes;
- optional, timeout-bounded `nvidia-smi` enrichment for NVIDIA model, memory, and temperature telemetry;
- best-effort thermal-zone readings; unavailable sensors are `UNKNOWN`, never zero; and
- a fixed loopback-only Ollama `/api/tags` request, including installed-model size metadata when supplied by Ollama.

`marshal doctor` reports the resource summary and Ollama model count. The authenticated Web endpoint `GET /api/v1/resources` returns the same point-in-time snapshot for the Operations page. It does not include environment variables, credentials, device serials, UUIDs, or raw vendor command output.

## Health and recommendations

Health is `OK`, `WARN`, `CRITICAL`, or `UNKNOWN`. Low available RAM, sustained swap use, low disk space, and trustworthy thermal readings can produce warnings. A missing source is `UNKNOWN` and is not treated as a measurement.

The `Safe` concurrency recommendation retains operating-system/MARSHAL headroom and respects lower cgroup limits over host capacity. It is an operator-visible default only; Community does not auto-scale, continuously retune workloads, force provider changes, or implement aggressive/performance modes.

Installed local models are classified as `RECOMMENDED`, `MAY_FIT`, `NOT_RECOMMENDED`, or `UNKNOWN` from their reported footprint and conservative available-memory/VRAM reserve. This is a hardware-fit signal, not a benchmark or task-quality claim.

## Limits and boundary

GPU and thermal discovery are optional. CPU-only systems, missing vendor CLIs, unavailable Ollama, malformed sysfs/API responses, and containers without telemetry continue safely with unavailable/`UNKNOWN` fields. Intel Arc, AMD, and NVIDIA cards are inventoried from DRM without a vendor utility; `nvidia-smi` only enriches NVIDIA results when present. Integrated/shared-memory GPUs deliberately report `SHARED_OR_UNKNOWN` rather than a fabricated zero-VRAM value. Localhost discovery does not relax egress controls and no network destinations are inspected.

Enterprise-only features remain intentionally absent: adaptive governors, continuous optimization, fleet placement, cross-worker telemetry, GPU packing, automatic model migration, and organization-level model economics.
