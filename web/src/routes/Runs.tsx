import { useState, useEffect, useCallback } from 'react';
import { api } from '../api/client';
import { useRealtimeEvent } from '../realtime/useRealtime';
import { StatusBadge, Button } from '../components/ui';
import { LoadingState, ErrorState, EmptyState } from '../components/state';
import { RunDetail } from './RunDetail';

interface RunItem {
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
}

interface RunsProps {
  initialTaskId?: string;
}

export function Runs({ initialTaskId }: RunsProps) {
  const [runs, setRuns] = useState<RunItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [taskFilter, setTaskFilter] = useState(initialTaskId ?? '');
  const [statusFilter, setStatusFilter] = useState('all');
  const [selectedRunId, setSelectedRunId] = useState<string | null>(null);

  const fetchRuns = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const resp = await api.listRuns({
        task_id: taskFilter.trim() ? taskFilter.trim() : undefined,
        status: statusFilter !== 'all' ? statusFilter : undefined,
      });
      setRuns(resp.items);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to fetch execution runs');
    } finally {
      setLoading(false);
    }
  }, [taskFilter, statusFilter]);

  useEffect(() => {
    void fetchRuns();
  }, [fetchRuns]);

  // Realtime updates
  useRealtimeEvent('task.status', () => {
    void fetchRuns();
  });

  const getStatusBadge = (status: string) => {
    switch (status) {
      case 'succeeded':
        return <StatusBadge status="ready" label="SUCCEEDED" />;
      case 'running':
        return <StatusBadge status="ready" label="RUNNING" />;
      case 'failed':
        return <StatusBadge status="degraded" label="FAILED" />;
      case 'canceled':
        return <StatusBadge status="degraded" label="CANCELED" />;
      default:
        return <StatusBadge status="degraded" label={status.toUpperCase()} />;
    }
  };

  return (
    <div className="runs-container">
      <div className="runs-header">
        <div className="runs-headline">
          <h2 className="runs-title">Execution Run Explorer</h2>
          <span className="runs-count">{runs.length} Recorded Runs</span>
        </div>
        <Button variant="secondary" size="sm" onClick={fetchRuns}>
          Refresh Runs
        </Button>
      </div>

      {/* Filter Toolbar */}
      <div className="filter-toolbar">
        <div className="filter-group">
          <input
            type="text"
            className="filter-input"
            placeholder="Filter by Task ID…"
            value={taskFilter}
            onChange={(e) => setTaskFilter(e.target.value)}
            aria-label="Filter runs by task ID"
          />
        </div>

        <div className="filter-group">
          <select
            className="filter-select"
            value={statusFilter}
            onChange={(e) => setStatusFilter(e.target.value)}
            aria-label="Filter runs by status"
          >
            <option value="all">All Statuses</option>
            <option value="running">Running</option>
            <option value="succeeded">Succeeded</option>
            <option value="failed">Failed</option>
            <option value="canceled">Canceled</option>
          </select>
        </div>
      </div>

      {/* Main Runs Table */}
      {loading ? (
        <LoadingState message="Loading execution telemetry & step events…" />
      ) : error ? (
        <ErrorState severity="error" message={error} onRetry={fetchRuns} />
      ) : runs.length === 0 ? (
        <EmptyState
          title="No execution runs found"
          description="Try adjusting your task or status filter to see recorded runs."
        />
      ) : (
        <div className="table-responsive">
          <table className="data-table" aria-label="Execution Runs Table">
            <thead>
              <tr>
                <th>Run ID</th>
                <th>Task ID</th>
                <th>Status</th>
                <th>Assigned Agent</th>
                <th>Duration</th>
                <th>Steps</th>
                <th>Evidence</th>
                <th>Commits</th>
              </tr>
            </thead>
            <tbody>
              {runs.map((r) => (
                <tr key={r.run_id} onClick={() => setSelectedRunId(r.run_id)} style={{ cursor: 'pointer' }}>
                  <td>
                    <code className="run-id-code">{r.run_id}</code>
                  </td>
                  <td>
                    <code className="task-id-code">{r.task_id}</code>
                  </td>
                  <td>{getStatusBadge(r.status)}</td>
                  <td>
                    <span className="font-mono text-xs">{r.agent_id}</span>
                  </td>
                  <td>
                    <span className="font-mono text-xs text-dim">{r.duration_ms}ms</span>
                  </td>
                  <td>
                    <span className="badge-steps">{r.step_count}</span>
                  </td>
                  <td>
                    <span className="badge-evidence font-mono text-xs">
                      {r.evidence_count} items
                    </span>
                  </td>
                  <td>
                    <span className="commit-range font-mono text-xs">
                      {r.base_commit.slice(0, 7)} → {r.head_commit.slice(0, 7)}
                    </span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {selectedRunId && (
        <RunDetail runId={selectedRunId} onClose={() => setSelectedRunId(null)} />
      )}
    </div>
  );
}
