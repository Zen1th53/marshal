import { useState, useEffect, useCallback } from 'react';
import { api } from '../api/client';
import { useRealtimeEvent } from '../realtime/useRealtime';
import { StatusBadge, Button } from '../components/ui';
import { LoadingState, ErrorState } from '../components/state';

interface OverviewData {
  system_status: {
    state: string;
    version: string;
    commit_sha: string;
    database_schema: string;
    active_workers: number;
    pending_tasks: number;
    updated_at: string;
  };
  tasks: {
    active: number;
    queued: number;
    blocked: number;
    review: number;
    completed: number;
    failed: number;
    total: number;
  };
  agents: {
    total: number;
    active: number;
    idle: number;
  };
  providers: Array<{
    name: string;
    binary_name: string;
    installed: boolean;
    state: string;
    version?: string;
    probed_at: string;
  }>;
  memory_health: string;
  security_notices: Array<{
    level: string;
    title: string;
    message: string;
    created_at: string;
  }>;
  evaluated_at: string;
}

interface OverviewProps {
  onNavigate?: (routeId: string) => void;
}

export function Overview({ onNavigate }: OverviewProps) {
  const [data, setData] = useState<OverviewData | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const fetchOverview = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const resp = await api.getOverview();
      setData(resp);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to load mission control overview');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void fetchOverview();
  }, [fetchOverview]);

  // Realtime updates
  useRealtimeEvent('task.status', () => {
    void fetchOverview();
  });
  useRealtimeEvent('system.status', () => {
    void fetchOverview();
  });

  if (loading && !data) {
    return <LoadingState message="Collecting mission control telemetry…" />;
  }

  if (error && !data) {
    return <ErrorState severity="error" message={error} onRetry={fetchOverview} />;
  }

  if (!data) return null;

  return (
    <div className="overview-container">
      <div className="overview-header">
        <div className="overview-headline">
          <h2 className="overview-title">Mission Control Overview</h2>
          <span className="overview-freshness">
            Telemetry Evaluated: {new Date(data.evaluated_at).toLocaleTimeString()}
          </span>
        </div>
        <Button variant="secondary" size="sm" onClick={fetchOverview}>
          Refresh Telemetry
        </Button>
      </div>

      {/* System & Memory Status Strip */}
      <div className="overview-status-strip">
        <div className="status-strip-card">
          <span className="strip-label">Runtime State</span>
          <StatusBadge
            status={data.system_status?.state === 'READY' ? 'ready' : 'degraded'}
            label={data.system_status?.state || 'UNKNOWN'}
          />
        </div>
        <div className="status-strip-card">
          <span className="strip-label">Version / Commit</span>
          <code className="strip-code">v{data.system_status?.version || '1.0.1'} ({data.system_status?.commit_sha || 'unknown'})</code>
        </div>
        <div className="status-strip-card">
          <span className="strip-label">DB Schema</span>
          <code className="strip-code">{data.system_status?.database_schema || 'v69'}</code>
        </div>
        <div className="status-strip-card">
          <span className="strip-label">Memory Subsystem</span>
          <StatusBadge status="ready" label={data.memory_health || 'READY'} />
        </div>
      </div>

      {/* Task Metric Summary Grid */}
      <div className="metric-grid">
        <div className="metric-card" onClick={() => onNavigate?.('tasks')} role="button" tabIndex={0}>
          <span className="metric-title">Active Tasks</span>
          <span className="metric-value">{data.tasks?.active ?? 0}</span>
          <span className="metric-subtext">Currently executing in workers</span>
        </div>
        <div className="metric-card" onClick={() => onNavigate?.('tasks')} role="button" tabIndex={0}>
          <span className="metric-title">Queued / Ready</span>
          <span className="metric-value">{data.tasks?.queued ?? 0}</span>
          <span className="metric-subtext">Waiting for scheduler execution</span>
        </div>
        <div className="metric-card" onClick={() => onNavigate?.('review')} role="button" tabIndex={0}>
          <span className="metric-title">Awaiting Review</span>
          <span className="metric-value">{data.tasks?.review ?? 0}</span>
          <span className="metric-subtext">Blocked at security / quorum gates</span>
        </div>
        <div className="metric-card" onClick={() => onNavigate?.('tasks')} role="button" tabIndex={0}>
          <span className="metric-title">Completed Tasks</span>
          <span className="metric-value">{data.tasks?.completed ?? 0}</span>
          <span className="metric-subtext">Verified with attestation proofs</span>
        </div>
      </div>

      {/* Two Column Layout: Providers & Security Notices */}
      <div className="overview-two-col">
        <div className="overview-section">
          <div className="section-header">
            <h3 className="section-title">Probed Adapter Providers</h3>
            <Button variant="ghost" size="sm" onClick={() => onNavigate?.('providers')}>
              Manage →
            </Button>
          </div>
          <div className="provider-list">
            {(data.providers || []).map((p) => (
              <div key={p.name} className="provider-item">
                <div className="provider-info">
                  <span className="provider-name">{p.name.toUpperCase()}</span>
                  <span className="provider-binary"><code>{p.binary_name}</code> (v{p.version || '0.0.0'})</span>
                </div>
                <StatusBadge status={p.state === 'READY' ? 'ready' : 'unavailable'} label={p.state} />
              </div>
            ))}
          </div>
        </div>

        <div className="overview-section">
          <div className="section-header">
            <h3 className="section-title">Active Security Notices</h3>
            <Button variant="ghost" size="sm" onClick={() => onNavigate?.('security')}>
              View Policy →
            </Button>
          </div>
          <div className="security-notice-list">
            {(data.security_notices || []).map((n, idx) => (
              <div key={idx} className="security-notice-item">
                <div className="notice-header">
                  <span className="notice-badge">[{n.level}]</span>
                  <span className="notice-title">{n.title}</span>
                </div>
                <p className="notice-message">{n.message}</p>
              </div>
            ))}
          </div>
        </div>
      </div>
    </div>
  );
}
