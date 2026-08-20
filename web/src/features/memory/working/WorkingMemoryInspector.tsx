import { useState, useEffect, useCallback } from 'react';
import { api } from '../../../api/client';
import { Button } from '../../../components/ui';
import { useToast } from '../../../components/toast';
import { LoadingState, ErrorState, EmptyState } from '../../../components/state';

interface WorkingSlot {
  slot_key: string;
  owner_scope: string;
  scope_id: string;
  content: string;
  revision: number;
  is_pinned: boolean;
  is_private: boolean;
  allocated_bytes: number;
  expires_at: string;
  last_updated_at: string;
}

interface WorkingMemoryData {
  slots: WorkingSlot[];
  total_quota_bytes: number;
  used_bytes: number;
  eviction_strategy: string;
}

export function WorkingMemoryInspector() {
  const [data, setData] = useState<WorkingMemoryData | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [promotingKey, setPromotingKey] = useState<string | null>(null);
  const { addToast } = useToast();

  const fetchWorkingMemory = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const resp = await api.getWorkingMemory();
      setData(resp);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to query working memory slots');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void fetchWorkingMemory();
  }, [fetchWorkingMemory]);

  const handlePromote = async (slot: WorkingSlot) => {
    setPromotingKey(slot.slot_key);
    try {
      const resp = await api.promoteWorkingSlot({
        slot_key: slot.slot_key,
        target_title: `Promoted: ${slot.slot_key}`,
      });
      addToast({
        type: 'success',
        message: resp.message,
      });
    } catch (err: unknown) {
      addToast({
        type: 'error',
        message: err instanceof Error ? err.message : 'Failed to promote scratchpad slot',
      });
    } finally {
      setPromotingKey(null);
    }
  };

  const handleTogglePin = async (slot: WorkingSlot) => {
    try {
      await api.updateWorkingSlot({
        slot_key: slot.slot_key,
        expected_revision: slot.revision,
        content: slot.content,
        is_pinned: !slot.is_pinned,
      });
      addToast({
        type: 'info',
        message: `Working slot pin state toggled to ${!slot.is_pinned}`,
      });
      await fetchWorkingMemory();
    } catch (err: unknown) {
      addToast({
        type: 'error',
        message: err instanceof Error ? err.message : 'CAS revision conflict',
      });
    }
  };

  if (loading) return <LoadingState message="Auditing scratchpad working memory slots and eviction quota…" />;
  if (error) return <ErrorState severity="error" message={error} onRetry={fetchWorkingMemory} />;
  if (!data) return null;

  const usagePct = ((data.used_bytes / data.total_quota_bytes) * 100).toFixed(1);

  return (
    <div className="working-memory-container">
      {/* Quota Progress Banner */}
      <div className="quota-meter-box" style={{ marginBottom: 'var(--space-3)' }}>
        <div className="quota-header">
          <span className="quota-title">Ephemeral Scratchpad Quota ({data.eviction_strategy} Eviction)</span>
          <span className="quota-values font-mono text-xs">
            {data.used_bytes} / {data.total_quota_bytes} Bytes ({usagePct}%)
          </span>
        </div>
        <div className="quota-bar-track">
          <div
            className="quota-bar-fill fill-primary"
            style={{ width: `${Math.min(Number(usagePct), 100)}%` }}
          />
        </div>
      </div>

      {/* Slots List */}
      {data.slots.length === 0 ? (
        <EmptyState
          title="No active working slots"
          description="Scratchpad working memory is currently clear. Agents will allocate ephemeral slots during reasoning."
        />
      ) : (
        <div className="working-slots-grid">
          {data.slots.map((s) => (
            <div key={s.slot_key} className="working-slot-card">
              <div className="working-slot-header">
                <div className="flex-row items-center gap-2">
                  <code className="slot-key-code font-mono text-xs">{s.slot_key}</code>
                  <span className="temporary-pill">TEMPORARY (r{s.revision})</span>
                </div>
                <div className="flex-row items-center gap-2">
                  {s.is_pinned && <span className="pinned-tag">PINNED</span>}
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => handleTogglePin(s)}
                    aria-label={s.is_pinned ? 'Unpin Slot' : 'Pin Slot'}
                  >
                    {s.is_pinned ? 'Unpin' : 'Pin'}
                  </Button>
                </div>
              </div>

              <div className="slot-content-body font-mono text-xs">
                {s.content}
              </div>

              <div className="working-slot-footer">
                <div className="slot-meta text-xs text-dim font-mono">
                  <span>Scope: {s.owner_scope} ({s.scope_id})</span>
                  <span> • {s.allocated_bytes} bytes</span>
                </div>
                <Button
                  variant="secondary"
                  size="sm"
                  disabled={promotingKey === s.slot_key}
                  onClick={() => handlePromote(s)}
                >
                  {promotingKey === s.slot_key ? 'Enqueuing…' : 'Promote to Candidate'}
                </Button>
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
