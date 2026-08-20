import { useState, useEffect, useCallback } from 'react';
import { api } from '../api/client';
import { StatusBadge, Button } from '../components/ui';
import { LoadingState, ErrorState } from '../components/state';
import { CorrelationLink } from '../components/audit/CorrelationLink';
import { SafeLogViewer, type LogLine } from '../features/logs/SafeLogViewer';

interface RunDetailProps {
  runId: string;
  onClose: () => void;
}

interface RunDetailData {
  run_id: string;
  task_id: string;
  agent_id: string;
  provider: string;
  status: string;
  duration_ms: number;
  step_count: number;
  evidence_count: number;
  base_commit: string;
  head_commit: string;
  started_at: string;
  finished_at?: string;
  correlation_id: string;
  summary: string;
  logs: LogLine[];
}

export function RunDetail({ runId, onClose }: RunDetailProps) {
  const [data, setData] = useState<RunDetailData | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const fetchRunDetail = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const resp = await api.getRunDetail(runId);
      setData(resp);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to fetch run details');
    } finally {
      setLoading(false);
    }
  }, [runId]);

  useEffect(() => {
    void fetchRunDetail();
  }, [fetchRunDetail]);

  return (
    <div className="modal-backdrop" onClick={onClose} role="dialog" aria-modal="true" aria-label="Run Detail Inspector">
      <div className="modal-card run-detail-card" onClick={(e) => e.stopPropagation()}>
        <div className="modal-header">
          <div className="task-detail-title-group">
            <h3 className="modal-title">Execution Run Telemetry</h3>
            <code className="task-id-badge">{runId}</code>
          </div>
          <button type="button" className="btn btn-ghost btn-sm" onClick={onClose} aria-label="Close">
            ✕
          </button>
        </div>

        <div className="modal-body">
          {loading ? (
            <LoadingState message="Loading run events and execution trace…" />
          ) : error ? (
            <ErrorState severity="error" message={error} onRetry={fetchRunDetail} />
          ) : data ? (
            <div className="run-detail-content">
              {/* Meta Grid */}
              <div className="task-meta-grid">
                <div className="meta-box">
                  <span className="meta-label">Status</span>
                  <StatusBadge
                    status={data.status === 'succeeded' || data.status === 'running' ? 'ready' : 'degraded'}
                    label={data.status.toUpperCase()}
                  />
                </div>
                <div className="meta-box">
                  <span className="meta-label">Task</span>
                  <code className="meta-value font-mono text-xs">{data.task_id}</code>
                </div>
                <div className="meta-box">
                  <span className="meta-label">Worker Agent</span>
                  <span className="meta-value font-mono text-xs">{data.agent_id}</span>
                </div>
                <div className="meta-box">
                  <span className="meta-label">Duration</span>
                  <span className="meta-value font-mono text-xs">{data.duration_ms}ms</span>
                </div>
              </div>

              {/* Commits & Audit Correlation */}
              <div className="task-commits-strip">
                <span className="meta-label">Commits:</span>
                <code>{data.base_commit.slice(0, 7)} → {data.head_commit.slice(0, 7)}</code>
                <span className="meta-label" style={{ marginLeft: 'auto' }}>Trace:</span>
                <CorrelationLink correlationId={data.correlation_id} />
              </div>

              {/* Terminal Log Viewer */}
              <div className="run-logs-wrapper">
                <h4 className="section-subtitle">Execution Step Logs</h4>
                <SafeLogViewer lines={data.logs} onRefresh={fetchRunDetail} />
              </div>
            </div>
          ) : null}
        </div>

        <div className="modal-actions">
          <Button variant="secondary" size="sm" onClick={onClose}>
            Close Inspector
          </Button>
        </div>
      </div>
    </div>
  );
}
