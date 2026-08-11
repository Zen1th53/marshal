# SLSA Provenance Profile

Agent OS aligns release provenance with SLSA 1.2 concepts:

- artifact subjects identified by digest,
- source/material identity,
- builder identity,
- build invocation,
- timestamps/environment as policy allows.

Do not claim a SLSA build level merely because a provenance JSON exists.

A real SLSA claim requires the corresponding build-system requirements and
verification.
