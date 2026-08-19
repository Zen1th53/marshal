# Vector Backends in MARSHAL (T102–T104)

## Architecture Overview

All dense retrieval in MARSHAL operates behind the `VectorBackend` contract defined in `internal/memory/index/vector/adapter.go`.

Derived vector stores are strictly **non-canonical** retrieval indices. If a vector backend crashes, corrupts, or is cleared, canonical SQLite memory remains completely intact.

## Supported Backends

1. **Local In-Memory Vector Store (`vector.LocalVectorStore`)**:
   - Zero external dependency backend used for unit testing, fast local execution, and development.
2. **SQLite-Vec Local (`sqlitevec.Backend`)**:
   - Primary default local embedding backend.
3. **TurboVec Optional High-Performance Backend (`turbovec.Backend`)**:
   - Optional high-performance filtered dense search adapter for high-scale memory corpora.
   - Evaluated to ensure no mandatory external Python dependencies are imposed on basic MARSHAL installations.
