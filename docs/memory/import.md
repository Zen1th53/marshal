# Retroactive Coding-Session Import (T94)

## Purpose

Enable importing existing local agent session histories (Claude, Codex, OpenCode, Deja Vu, Gemini) safely into MARSHAL memory without starting with an empty store.

## Governance Invariants

1. **Episodic & Candidate Only**: Imported transcripts enter as `MemoryKindEpisodic` with `Lifecycle = MemoryCandidate` and `Authority = AuthorityAgent`. They never enter as durable canonical authority.
2. **Pre-Persistence Secret Redaction/Rejection**: All imported transcripts pass through the Security Firewall (`T86`). Transcripts containing detected secrets are rejected or redacted before SQLite write.
3. **Deterministic Content-Digest Idempotency**: Repeated imports of identical historical transcripts are deduplicated by `CanonicalDigest()`.
