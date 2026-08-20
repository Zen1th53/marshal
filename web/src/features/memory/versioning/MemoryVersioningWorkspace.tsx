import { useState, useEffect, useCallback } from 'react';
import { api } from '../../../api/client';
import { StatusBadge, Button } from '../../../components/ui';
import { useToast } from '../../../components/toast';
import { LoadingState, ErrorState, EmptyState } from '../../../components/state';

interface Snapshot {
  snapshot_id: string;
  branch: string;
  manifest_digest_sha256: string;
  record_count: number;
  message: string;
  created_by: string;
  created_at: string;
}

interface DiffEntry {
  memory_id: string;
  change_type: string;
  old_title?: string;
  new_title?: string;
  details: string;
}

export function MemoryVersioningWorkspace() {
  const [snapshots, setSnapshots] = useState<Snapshot[]>([]);
  const [activeHead, setActiveHead] = useState('');
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [diffEntries, setDiffEntries] = useState<DiffEntry[]>([]);
  const [showDiff, setShowDiff] = useState(false);
  const [diffLoading, setDiffLoading] = useState(false);

  const [showCreateModal, setShowCreateModal] = useState(false);
  const [createMsg, setCreateMsg] = useState('');
  const [createBranch, setCreateBranch] = useState('main');

  const [rollbackTarget, setRollbackTarget] = useState<Snapshot | null>(null);
  const [rollbackReason, setRollbackReason] = useState('');
  const [rollingBack, setRollingBack] = useState(false);

  const { addToast } = useToast();

  const fetchSnapshots = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const resp = await api.listMemorySnapshots();
      setSnapshots(resp.snapshots);
      setActiveHead(resp.active_head);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to query memory snapshots');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void fetchSnapshots();
  }, [fetchSnapshots]);

  const handleCreateSnapshot = async () => {
    if (!createMsg.trim()) return;
    try {
      const resp = await api.createMemorySnapshot({
        branch: createBranch,
        message: createMsg,
      });
      addToast({
        type: 'success',
        message: `Created memory snapshot ${resp.snapshot_id}`,
      });
      setShowCreateModal(false);
      setCreateMsg('');
      await fetchSnapshots();
    } catch (err: unknown) {
      addToast({
        type: 'error',
        message: err instanceof Error ? err.message : 'Failed to create snapshot',
      });
    }
  };

  const handleInspectDiff = async () => {
    if (snapshots.length < 2) return;
    setDiffLoading(true);
    setShowDiff(true);
    try {
      const from = snapshots[0].snapshot_id;
      const to = snapshots[snapshots.length - 1].snapshot_id;
      const resp = await api.getMemorySnapshotDiff(from, to);
      setDiffEntries(resp.entries);
    } catch (err: unknown) {
      addToast({
        type: 'error',
        message: err instanceof Error ? err.message : 'Failed to query snapshot diff',
      });
    } finally {
      setDiffLoading(false);
    }
  };

  const handleRollback = async () => {
    if (!rollbackTarget || !rollbackReason.trim()) return;
    setRollingBack(true);
    try {
      const resp = await api.rollbackMemorySnapshot({
        target_snapshot_id: rollbackTarget.snapshot_id,
        reason: rollbackReason,
      });
      addToast({
        type: 'info',
        message: `Restored memory head to ${resp.target_snapshot_id} (Audit: ${resp.audit_id})`,
      });
      setRollbackTarget(null);
      setRollbackReason('');
      await fetchSnapshots();
    } catch (err: unknown) {
      addToast({
        type: 'error',
        message: err instanceof Error ? err.message : 'Failed to rollback snapshot',
      });
    } finally {
      setRollingBack(false);
    }
  };

  return (
    <div className="memory-versioning-container">
      <div className="memory-header">
        <div className="memory-headline">
          <h2 className="memory-title">Memory Versioning, Snapshots & Rollback</h2>
          <span className="memory-subtitle">
            Immutable manifest snapshots, branch timeline diffs, and non-destructive head restoration
          </span>
        </div>
        <div className="flex-row items-center gap-2">
          <Button variant="secondary" size="sm" onClick={handleInspectDiff}>
            Compare Head Diff
          </Button>
          <Button variant="primary" size="sm" onClick={() => setShowCreateModal(true)}>
            Create Snapshot
          </Button>
        </div>
      </div>

      {loading ? (
        <LoadingState message="Auditing snapshot commit tree and manifest SHA-256 digests…" />
      ) : error ? (
        <ErrorState severity="error" message={error} onRetry={fetchSnapshots} />
      ) : snapshots.length === 0 ? (
        <EmptyState
          title="No snapshots recorded"
          description="Create your first immutable memory snapshot checkpoint."
        />
      ) : (
        <div className="table-responsive">
          <table className="data-table" aria-label="Snapshots Table">
            <thead>
              <tr>
                <th>Snapshot ID</th>
                <th>Branch</th>
                <th>Manifest Digest</th>
                <th>Records</th>
                <th>Message</th>
                <th>Created</th>
                <th>Action</th>
              </tr>
            </thead>
            <tbody>
              {snapshots.map((snap) => {
                const isHead = snap.snapshot_id === activeHead;
                return (
                  <tr key={snap.snapshot_id}>
                    <td>
                      <div className="flex-row items-center gap-2">
                        <code className="font-mono text-xs font-bold">{snap.snapshot_id}</code>
                        {isHead && <StatusBadge status="ready" label="HEAD" />}
                      </div>
                    </td>
                    <td>
                      <code className="font-mono text-xs">{snap.branch}</code>
                    </td>
                    <td>
                      <code className="font-mono text-xs text-dim">
                        {snap.manifest_digest_sha256.slice(0, 16)}…
                      </code>
                    </td>
                    <td className="font-mono text-xs">{snap.record_count}</td>
                    <td className="text-xs text-muted description-cell">{snap.message}</td>
                    <td className="font-mono text-xs text-dim">
                      {new Date(snap.created_at).toLocaleTimeString()}
                    </td>
                    <td>
                      {!isHead && (
                        <Button
                          variant="secondary"
                          size="sm"
                          onClick={() => setRollbackTarget(snap)}
                        >
                          Rollback
                        </Button>
                      )}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      {/* Snapshot Diff Modal */}
      {showDiff && (
        <div className="modal-backdrop" onClick={() => setShowDiff(false)}>
          <div className="modal-card modal-lg" onClick={(e) => e.stopPropagation()}>
            <div className="modal-header">
              <h3 className="modal-title">Memory Snapshot Diff Breakdown</h3>
              <button
                type="button"
                className="btn btn-ghost btn-sm"
                onClick={() => setShowDiff(false)}
                aria-label="Close"
              >
                ✕
              </button>
            </div>

            <div className="modal-body">
              {diffLoading ? (
                <LoadingState message="Computing differential node deltas…" />
              ) : diffEntries.length === 0 ? (
                <EmptyState title="No changes" description="Identical snapshot trees." />
              ) : (
                <div className="diff-entries-list">
                  {diffEntries.map((e) => (
                    <div key={e.memory_id} className={`diff-entry-item change-${e.change_type}`}>
                      <div className="flex-row items-center gap-2">
                        <span className={`diff-tag tag-${e.change_type}`}>{e.change_type.toUpperCase()}</span>
                        <code className="font-mono text-xs">{e.memory_id}</code>
                        <span className="text-xs font-semibold">{e.new_title || e.old_title}</span>
                      </div>
                      <p className="text-xs text-dim" style={{ marginTop: 'var(--space-1)' }}>
                        {e.details}
                      </p>
                    </div>
                  ))}
                </div>
              )}
            </div>

            <div className="modal-actions">
              <Button variant="secondary" size="sm" onClick={() => setShowDiff(false)}>
                Close Diff
              </Button>
            </div>
          </div>
        </div>
      )}

      {/* Create Snapshot Modal */}
      {showCreateModal && (
        <div className="modal-backdrop" onClick={() => setShowCreateModal(false)}>
          <div className="modal-card" onClick={(e) => e.stopPropagation()}>
            <div className="modal-header">
              <h3 className="modal-title">Create Memory Corpus Snapshot</h3>
              <button
                type="button"
                className="btn btn-ghost btn-sm"
                onClick={() => setShowCreateModal(false)}
                aria-label="Close"
              >
                ✕
              </button>
            </div>

            <div className="modal-body">
              <div className="form-group" style={{ marginBottom: 'var(--space-3)' }}>
                <label className="form-label text-xs">Branch</label>
                <input
                  type="text"
                  className="form-input text-xs"
                  value={createBranch}
                  onChange={(e) => setCreateBranch(e.target.value)}
                />
              </div>

              <div className="form-group">
                <label className="form-label text-xs">Snapshot Checkpoint Message *</label>
                <textarea
                  className="form-input form-textarea text-xs"
                  rows={3}
                  placeholder="Describe memory corpus state..."
                  value={createMsg}
                  onChange={(e) => setCreateMsg(e.target.value)}
                  required
                />
              </div>
            </div>

            <div className="modal-actions">
              <Button variant="secondary" size="sm" onClick={() => setShowCreateModal(false)}>
                Cancel
              </Button>
              <Button
                variant="primary"
                size="sm"
                disabled={!createMsg.trim()}
                onClick={handleCreateSnapshot}
              >
                Commit Snapshot
              </Button>
            </div>
          </div>
        </div>
      )}

      {/* Rollback Confirmation Modal */}
      {rollbackTarget && (
        <div className="modal-backdrop" onClick={() => setRollbackTarget(null)}>
          <div className="modal-card" onClick={(e) => e.stopPropagation()}>
            <div className="modal-header">
              <h3 className="modal-title">Rollback Head to Snapshot</h3>
              <button
                type="button"
                className="btn btn-ghost btn-sm"
                onClick={() => setRollbackTarget(null)}
                aria-label="Close"
              >
                ✕
              </button>
            </div>

            <div className="modal-body">
              <div className="form-group" style={{ marginBottom: 'var(--space-3)' }}>
                <label className="form-label text-xs">Target Snapshot ID</label>
                <code className="font-mono text-xs">{rollbackTarget.snapshot_id}</code>
              </div>

              <div className="form-group">
                <label className="form-label text-xs">Rollback Reason / Justification *</label>
                <textarea
                  className="form-input form-textarea text-xs"
                  rows={3}
                  placeholder="Explain why the current head is being reverted..."
                  value={rollbackReason}
                  onChange={(e) => setRollbackReason(e.target.value)}
                  required
                />
              </div>
            </div>

            <div className="modal-actions">
              <Button variant="secondary" size="sm" onClick={() => setRollbackTarget(null)}>
                Cancel
              </Button>
              <Button
                variant="danger"
                size="sm"
                disabled={rollingBack || !rollbackReason.trim()}
                onClick={handleRollback}
              >
                {rollingBack ? 'Restoring…' : 'Confirm Rollback'}
              </Button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
