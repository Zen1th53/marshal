import { useState } from 'react';
import { api } from '../../../api/client';
import { Button } from '../../../components/ui';
import { useToast } from '../../../components/toast';

interface MemoryMutationActionsProps {
  memoryId: string;
  revision: number;
  digestSha256?: string;
  lifecycle: string;
  onMutated: () => void;
}

export function MemoryMutationActions({
  memoryId,
  revision,
  digestSha256,
  lifecycle,
  onMutated,
}: MemoryMutationActionsProps) {
  const [loading, setLoading] = useState(false);
  const [showPromoteModal, setShowPromoteModal] = useState(false);
  const [rationale, setRationale] = useState('');
  const [authority, setAuthority] = useState('verified');
  const { addToast } = useToast();

  const handlePromote = async () => {
    if (!rationale.trim()) return;
    setLoading(true);
    try {
      const resp = await api.promoteMemory({
        memory_id: memoryId,
        expected_revision: revision,
        expected_digest_sha256: digestSha256,
        assigned_authority: authority,
        review_rationale: rationale,
      });
      addToast({
        type: 'success',
        message: `Memory node promoted to durable active state (Audit ID: ${resp.audit_id})`,
      });
      setShowPromoteModal(false);
      onMutated();
    } catch (err: unknown) {
      addToast({
        type: 'error',
        message: err instanceof Error ? err.message : 'Failed to promote memory node',
      });
    } finally {
      setLoading(false);
    }
  };

  const handleTombstone = async () => {
    if (!window.confirm(`Are you sure you want to tombstone and purge memory ${memoryId}?`)) {
      return;
    }
    setLoading(true);
    try {
      const resp = await api.tombstoneMemory({
        target_memory_id: memoryId,
        expected_revision: revision,
        reason: 'Operator tombstone action from Memory Explorer',
      });
      addToast({
        type: 'info',
        message: `Memory node marked for tombstone eviction (Audit ID: ${resp.audit_id})`,
      });
      onMutated();
    } catch (err: unknown) {
      addToast({
        type: 'error',
        message: err instanceof Error ? err.message : 'Failed to tombstone memory node',
      });
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="memory-mutation-actions flex-row items-center gap-2">
      {lifecycle !== 'active' && (
        <Button variant="primary" size="sm" onClick={() => setShowPromoteModal(true)}>
          Promote to Durable
        </Button>
      )}

      {lifecycle !== 'evicted' && (
        <Button variant="danger" size="sm" disabled={loading} onClick={handleTombstone}>
          Tombstone / Purge
        </Button>
      )}

      {/* Promotion Rationale Modal */}
      {showPromoteModal && (
        <div className="modal-backdrop" onClick={() => setShowPromoteModal(false)}>
          <div className="modal-card" onClick={(e) => e.stopPropagation()}>
            <div className="modal-header">
              <h3 className="modal-title">Promote Memory Candidate to Durable Truth</h3>
              <button
                type="button"
                className="btn btn-ghost btn-sm"
                onClick={() => setShowPromoteModal(false)}
                aria-label="Close"
              >
                ✕
              </button>
            </div>

            <div className="modal-body">
              <div className="form-group" style={{ marginBottom: 'var(--space-3)' }}>
                <label className="form-label text-xs">Target Memory ID</label>
                <code className="font-mono text-xs">{memoryId}</code>
              </div>

              <div className="form-group" style={{ marginBottom: 'var(--space-3)' }}>
                <label className="form-label text-xs">Assigned Authority Tier</label>
                <select
                  className="form-input form-select text-xs"
                  value={authority}
                  onChange={(e) => setAuthority(e.target.value)}
                >
                  <option value="verified">Verified (Multi-Agent Attested)</option>
                  <option value="provisional">Provisional (Candidate Scope)</option>
                </select>
              </div>

              <div className="form-group">
                <label className="form-label text-xs">Review Rationale & Justification *</label>
                <textarea
                  className="form-input form-textarea text-xs"
                  rows={3}
                  placeholder="Explain why this belief/procedure invariant meets safety and verification criteria…"
                  value={rationale}
                  onChange={(e) => setRationale(e.target.value)}
                  required
                />
              </div>
            </div>

            <div className="modal-actions">
              <Button variant="secondary" size="sm" onClick={() => setShowPromoteModal(false)}>
                Cancel
              </Button>
              <Button
                variant="primary"
                size="sm"
                disabled={loading || !rationale.trim()}
                onClick={handlePromote}
              >
                {loading ? 'Promoting…' : 'Confirm Promotion'}
              </Button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
