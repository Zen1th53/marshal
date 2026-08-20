import { useState, useEffect, useCallback } from 'react';
import { api } from '../../../api/client';
import { StatusBadge, Button } from '../../../components/ui';
import { LoadingState, ErrorState, EmptyState } from '../../../components/state';

interface UsageEvent {
  event_id: string;
  event_type: string;
  task_id: string;
  run_id: string;
  agent_id: string;
  revision_used: number;
  evidence_plan_id?: string;
  causal_link_status: string;
  timestamp: string;
}

interface UsageTraceData {
  memory_id: string;
  title: string;
  total_recalls: number;
  total_injections: number;
  total_citations: number;
  events: UsageEvent[];
}

interface MemoryInfluenceTraceProps {
  memoryId: string;
  onClose: () => void;
}

export function MemoryInfluenceTrace({ memoryId, onClose }: MemoryInfluenceTraceProps) {
  const [data, setData] = useState<UsageTraceData | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const fetchTrace = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const resp = await api.getMemoryUsageTrace(memoryId);
      setData(resp);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to query memory influence trace');
    } finally {
      setLoading(false);
    }
  }, [memoryId]);

  useEffect(() => {
    void fetchTrace();
  }, [fetchTrace]);

  return (
    <div className="modal-backdrop" onClick={onClose} role="dialog" aria-modal="true" aria-label="Influence Trace Modal">
      <div className="modal-card run-detail-card" onClick={(e) => e.stopPropagation()}>
        <div className="modal-header">
          <div className="task-detail-title-group">
            <h3 className="modal-title">Memory Read Receipts & Causal Influence Trace</h3>
            <code className="font-mono text-xs">{memoryId}</code>
          </div>
          <button type="button" className="btn btn-ghost btn-sm" onClick={onClose} aria-label="Close">
            ✕
          </button>
        </div>

        <div className="modal-body">
          {loading ? (
            <LoadingState message="Auditing recall logs, prompt injection receipts, and agent action citations…" />
          ) : error ? (
            <ErrorState severity="error" message={error} onRetry={fetchTrace} />
          ) : data ? (
            <div className="influence-trace-content">
              {/* Metric Highlights */}
              <div className="task-meta-grid" style={{ marginBottom: 'var(--space-3)' }}>
                <div className="meta-box">
                  <span className="meta-label">Total Recalls</span>
                  <span className="meta-value font-mono text-xs">{data.total_recalls}</span>
                </div>
                <div className="meta-box">
                  <span className="meta-label">Prompt Injections</span>
                  <span className="meta-value font-mono text-xs">{data.total_injections}</span>
                </div>
                <div className="meta-box">
                  <span className="meta-label">Action Citations</span>
                  <span className="meta-value font-mono text-xs font-bold text-accent">
                    {data.total_citations}
                  </span>
                </div>
              </div>

              {/* Events Table */}
              {data.events.length === 0 ? (
                <EmptyState
                  title="No Recall Receipts"
                  description="This memory node has not yet been recalled into an agent prompt or reasoning context."
                />
              ) : (
                <div className="table-responsive">
                  <table className="data-table" aria-label="Read Receipts Table">
                    <thead>
                      <tr>
                        <th>Receipt ID</th>
                        <th>Event Type</th>
                        <th>Causal Attribution</th>
                        <th>Agent / Task</th>
                        <th>Run ID</th>
                        <th>Revision</th>
                        <th>Timestamp</th>
                      </tr>
                    </thead>
                    <tbody>
                      {data.events.map((ev) => (
                        <tr key={ev.event_id}>
                          <td>
                            <code className="font-mono text-xs">{ev.event_id}</code>
                          </td>
                          <td>
                            <span className={`receipt-type-pill type-${ev.event_type}`}>
                              {ev.event_type.replace(/_/g, ' ').toUpperCase()}
                            </span>
                          </td>
                          <td>
                            <StatusBadge
                              status={
                                ev.causal_link_status === 'direct_citation'
                                  ? 'ready'
                                  : ev.causal_link_status === 'injected_context'
                                    ? 'pending'
                                    : 'degraded'
                              }
                              label={ev.causal_link_status.replace(/_/g, ' ').toUpperCase()}
                            />
                          </td>
                          <td>
                            <div className="flex-col">
                              <span className="font-semibold text-xs">{ev.agent_id}</span>
                              <code className="font-mono text-xs text-dim">{ev.task_id}</code>
                            </div>
                          </td>
                          <td>
                            <code className="font-mono text-xs text-accent">{ev.run_id}</code>
                          </td>
                          <td className="font-mono text-xs">r{ev.revision_used}</td>
                          <td className="font-mono text-xs text-dim">
                            {new Date(ev.timestamp).toLocaleTimeString()}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </div>
          ) : null}
        </div>

        <div className="modal-actions">
          <Button variant="secondary" size="sm" onClick={onClose}>
            Close
          </Button>
        </div>
      </div>
    </div>
  );
}
