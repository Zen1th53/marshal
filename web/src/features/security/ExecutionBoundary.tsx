import { useState, useEffect, useCallback } from 'react';
import { api } from '../../api/client';
import { StatusBadge } from '../../components/ui';
import { LoadingState, ErrorState } from '../../components/state';

interface ExecutionBoundaryProps {
  runId: string;
}

interface ResourceQuota {
  limit: number;
  used: number;
  unit: string;
  usage_pct: number;
}

interface ExecutionBoundaryData {
  run_id: string;
  sandbox_backend: string;
  backend_status: string;
  network_policy: string;
  is_network_isolated: boolean;
  cpu_quota_pct: number;
  memory: ResourceQuota;
  pids: ResourceQuota;
  disk: ResourceQuota;
  was_oom_killed: boolean;
  was_pid_exhausted: boolean;
  was_disk_exhausted: boolean;
  mounted_paths: string[];
  audited_at: string;
}

export function ExecutionBoundary({ runId }: ExecutionBoundaryProps) {
  const [data, setData] = useState<ExecutionBoundaryData | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const fetchBoundary = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const resp = await api.getRunBoundary(runId);
      setData(resp);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to query execution boundary status');
    } finally {
      setLoading(false);
    }
  }, [runId]);

  useEffect(() => {
    void fetchBoundary();
  }, [fetchBoundary]);

  if (loading) return <LoadingState message="Auditing sandbox isolation & resource quotas…" />;
  if (error) return <ErrorState severity="error" message={error} onRetry={fetchBoundary} />;
  if (!data) return null;

  return (
    <div className="execution-boundary-wrapper" role="region" aria-label="Sandbox & Resource Boundary Details">
      {/* Sandbox & Network Status Header */}
      <div className="boundary-status-card">
        <div className="task-meta-grid">
          <div className="meta-box">
            <span className="meta-label">Sandbox Backend</span>
            <div className="flex-row items-center gap-2">
              <code className="font-mono text-xs">{data.sandbox_backend.toUpperCase()}</code>
              <StatusBadge
                status={data.backend_status === 'enforced' ? 'ready' : 'degraded'}
                label={data.backend_status.toUpperCase()}
              />
            </div>
          </div>
          <div className="meta-box">
            <span className="meta-label">Network Egress</span>
            <StatusBadge
              status={data.is_network_isolated ? 'ready' : 'degraded'}
              label={data.is_network_isolated ? 'AIRGAPPED / ISOLATED' : 'NETWORK UNRESTRICTED'}
            />
          </div>
          <div className="meta-box">
            <span className="meta-label">CPU Quota</span>
            <span className="meta-value font-mono text-xs">{data.cpu_quota_pct}% CPU</span>
          </div>
        </div>
      </div>

      {/* Resource Quota Progress Bars */}
      <div className="resource-quotas-section">
        <h4 className="section-subtitle">Resource Enforcement Gauges</h4>
        <div className="quotas-grid">
          {/* Memory */}
          <div className="quota-meter-box">
            <div className="quota-header">
              <span className="quota-title">Memory Allocation</span>
              <span className="quota-values font-mono text-xs">
                {data.memory.used} / {data.memory.limit} {data.memory.unit} ({data.memory.usage_pct}%)
              </span>
            </div>
            <div className="quota-bar-track">
              <div
                className={`quota-bar-fill ${data.was_oom_killed ? 'fill-danger' : 'fill-primary'}`}
                style={{ width: `${Math.min(data.memory.usage_pct, 100)}%` }}
              />
            </div>
            {data.was_oom_killed && (
              <span className="exhaustion-warning">⚠️ PROCESS TERMINATED DUE TO OOM</span>
            )}
          </div>

          {/* PIDs */}
          <div className="quota-meter-box">
            <div className="quota-header">
              <span className="quota-title">PID Slots</span>
              <span className="quota-values font-mono text-xs">
                {data.pids.used} / {data.pids.limit} {data.pids.unit} ({data.pids.usage_pct}%)
              </span>
            </div>
            <div className="quota-bar-track">
              <div
                className="quota-bar-fill fill-primary"
                style={{ width: `${Math.min(data.pids.usage_pct, 100)}%` }}
              />
            </div>
          </div>

          {/* Ephemeral Disk */}
          <div className="quota-meter-box">
            <div className="quota-header">
              <span className="quota-title">Ephemeral Disk</span>
              <span className="quota-values font-mono text-xs">
                {data.disk.used} / {data.disk.limit} {data.disk.unit} ({data.disk.usage_pct}%)
              </span>
            </div>
            <div className="quota-bar-track">
              <div
                className="quota-bar-fill fill-primary"
                style={{ width: `${Math.min(data.disk.usage_pct, 100)}%` }}
              />
            </div>
          </div>
        </div>
      </div>

      {/* Redacted Mount Points */}
      <div className="mounts-section">
        <h4 className="section-subtitle">Sandboxed Filesystem Mounts ({data.mounted_paths.length})</h4>
        <div className="mounts-list">
          {data.mounted_paths.map((p, idx) => (
            <div key={idx} className="mount-item font-mono text-xs">
              <span className="mount-icon">📁</span>
              <code>{p}</code>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
