import { useState, useEffect, useCallback } from 'react';
import { api } from '../api/client';
import { StatusBadge, Button } from '../components/ui';
import { LoadingState, ErrorState, EmptyState } from '../components/state';

interface GovernanceItem {
  id: string;
  category: string;
  status: string;
  target_memory_id: string;
  conflict_with_id?: string;
  reason: string;
  detected_at: string;
}

interface ConflictComparisonData {
  conflict_id: string;
  status: string;
  resolution_mode: string;
  base_memory: {
    id: string;
    title: string;
    body: string;
    authority: string;
    confidence: number;
    scope: string;
    kind: string;
    observed_at: string;
  };
  competing_memory: {
    id: string;
    title: string;
    body: string;
    authority: string;
    confidence: number;
    scope: string;
    kind: string;
    observed_at: string;
  };
  detected_at: string;
}

export function MemoryGovernance() {
  const [items, setItems] = useState<GovernanceItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [categoryFilter, setCategoryFilter] = useState('all');

  const [activeConflict, setActiveConflict] = useState<ConflictComparisonData | null>(null);
  const [comparing, setComparing] = useState(false);

  const fetchQueue = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const resp = await api.listGovernanceQueue(categoryFilter !== 'all' ? categoryFilter : undefined);
      setItems(resp.items);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to query memory governance queue');
    } finally {
      setLoading(false);
    }
  }, [categoryFilter]);

  useEffect(() => {
    void fetchQueue();
  }, [fetchQueue]);

  const handleInspectConflict = async (conflictId: string) => {
    setComparing(true);
    try {
      const resp = await api.getConflictComparison(conflictId);
      setActiveConflict(resp);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to load conflict comparison');
    } finally {
      setComparing(false);
    }
  };

  return (
    <div className="memory-governance-container">
      <div className="memory-header">
        <div className="memory-headline">
          <h2 className="memory-title">Memory Governance, Stale & Conflict Workspace</h2>
          <span className="memory-subtitle">
            Curate conflicting beliefs, stale records, supersession lineage and tombstone purges
          </span>
        </div>
        <Button variant="secondary" size="sm" onClick={fetchQueue}>
          Refresh Queue
        </Button>
      </div>

      {/* Preset Category Tabs */}
      <div className="filter-presets">
        <button
          type="button"
          className={`preset-tab ${categoryFilter === 'all' ? 'active' : ''}`}
          onClick={() => setCategoryFilter('all')}
        >
          All Items ({items.length})
        </button>
        <button
          type="button"
          className={`preset-tab ${categoryFilter === 'conflicted' ? 'active' : ''}`}
          onClick={() => setCategoryFilter('conflicted')}
        >
          ⚠️ Conflicted
        </button>
        <button
          type="button"
          className={`preset-tab ${categoryFilter === 'stale' ? 'active' : ''}`}
          onClick={() => setCategoryFilter('stale')}
        >
          ⏳ Stale TTL
        </button>
        <button
          type="button"
          className={`preset-tab ${categoryFilter === 'superseded' ? 'active' : ''}`}
          onClick={() => setCategoryFilter('superseded')}
        >
          🗂️ Superseded
        </button>
        <button
          type="button"
          className={`preset-tab ${categoryFilter === 'forgetting' ? 'active' : ''}`}
          onClick={() => setCategoryFilter('forgetting')}
        >
          🗑️ Tombstones
        </button>
      </div>

      {/* Queue Content */}
      {loading ? (
        <LoadingState message="Auditing governance queues, active conflicts, and tombstone lifecycle…" />
      ) : error ? (
        <ErrorState severity="error" message={error} onRetry={fetchQueue} />
      ) : items.length === 0 ? (
        <EmptyState
          title="Governance Queue Clean"
          description="No conflicted, stale or superseded memory records require operator attention."
        />
      ) : (
        <div className="table-responsive">
          <table className="data-table" aria-label="Governance Queue Table">
            <thead>
              <tr>
                <th>Queue Item ID</th>
                <th>Category</th>
                <th>Target Node</th>
                <th>Conflict Target</th>
                <th>Governance Reason</th>
                <th>Detected At</th>
                <th>Action</th>
              </tr>
            </thead>
            <tbody>
              {items.map((it) => (
                <tr key={it.id}>
                  <td>
                    <code className="gov-id-code font-mono text-xs">{it.id}</code>
                  </td>
                  <td>
                    <span className={`gov-category-badge cat-${it.category}`}>{it.category.toUpperCase()}</span>
                  </td>
                  <td>
                    <code className="font-mono text-xs">{it.target_memory_id}</code>
                  </td>
                  <td>
                    {it.conflict_with_id ? (
                      <code className="font-mono text-xs text-danger">{it.conflict_with_id}</code>
                    ) : (
                      <span className="text-dim">—</span>
                    )}
                  </td>
                  <td className="text-xs text-muted description-cell">{it.reason}</td>
                  <td className="font-mono text-xs text-dim">
                    {new Date(it.detected_at).toLocaleTimeString()}
                  </td>
                  <td>
                    {it.category === 'conflicted' ? (
                      <Button
                        variant="primary"
                        size="sm"
                        disabled={comparing}
                        onClick={() => handleInspectConflict(it.id)}
                      >
                        Compare Diff
                      </Button>
                    ) : (
                      <StatusBadge
                        status={it.status === 'resolved' || it.status === 'purged' ? 'ready' : 'degraded'}
                        label={it.status.toUpperCase()}
                      />
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {/* Conflict Diff Comparison Modal */}
      {activeConflict && (
        <div className="modal-backdrop" onClick={() => setActiveConflict(null)}>
          <div className="modal-card modal-lg" onClick={(e) => e.stopPropagation()}>
            <div className="modal-header">
              <div className="task-detail-title-group">
                <h3 className="modal-title">Side-by-Side Memory Conflict Diff</h3>
                <code className="task-id-badge">{activeConflict.conflict_id}</code>
              </div>
              <button
                type="button"
                className="btn btn-ghost btn-sm"
                onClick={() => setActiveConflict(null)}
                aria-label="Close"
              >
                ✕
              </button>
            </div>

            <div className="modal-body">
              <div className="conflict-diff-grid">
                {/* Base Memory Column */}
                <div className="conflict-side-card base-side">
                  <div className="conflict-side-header">
                    <span className="side-title font-semibold text-xs">BASE RECORD (Current)</span>
                    <StatusBadge
                      status={activeConflict.base_memory.authority === 'verified' ? 'ready' : 'degraded'}
                      label={activeConflict.base_memory.authority.toUpperCase()}
                    />
                  </div>
                  <h4 className="font-semibold text-sm">{activeConflict.base_memory.title}</h4>
                  <code className="font-mono text-xs">{activeConflict.base_memory.id}</code>
                  <div className="code-block font-mono text-xs" style={{ marginTop: 'var(--space-2)' }}>
                    {activeConflict.base_memory.body}
                  </div>
                </div>

                {/* Competing Memory Column */}
                <div className="conflict-side-card competing-side">
                  <div className="conflict-side-header">
                    <span className="side-title font-semibold text-xs text-danger">COMPETING RECORD</span>
                    <StatusBadge
                      status={activeConflict.competing_memory.authority === 'verified' ? 'ready' : 'degraded'}
                      label={activeConflict.competing_memory.authority.toUpperCase()}
                    />
                  </div>
                  <h4 className="font-semibold text-sm">{activeConflict.competing_memory.title}</h4>
                  <code className="font-mono text-xs">{activeConflict.competing_memory.id}</code>
                  <div className="code-block font-mono text-xs" style={{ marginTop: 'var(--space-2)' }}>
                    {activeConflict.competing_memory.body}
                  </div>
                </div>
              </div>

              <div className="resolution-notice" style={{ marginTop: 'var(--space-3)' }}>
                <span className="text-xs font-mono text-dim">
                  Resolution Mode: <strong>{activeConflict.resolution_mode}</strong> (Automatic AI score resolution is prohibited by safety policy)
                </span>
              </div>
            </div>

            <div className="modal-actions">
              <Button variant="secondary" size="sm" onClick={() => setActiveConflict(null)}>
                Close Diff
              </Button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
