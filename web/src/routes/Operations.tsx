import { useState, useEffect, useCallback } from 'react';
import { api } from '../api/client';
import { StatusBadge, Button } from '../components/ui';
import { LoadingState, ErrorState } from '../components/state';
import { BackupRestoreWorkspace } from '../features/operations/backup/BackupRestoreWorkspace';
import { MaintenanceWorkspace } from '../features/operations/maintenance/MaintenanceWorkspace';
import { ReleaseTrustView } from '../features/operations/trust/ReleaseTrustView';

interface DiagnosticCheck {
  component: string;
  status: string;
  latency_ms: number;
  message: string;
}

interface DoctorReportData {
  overall_status: string;
  checks: DiagnosticCheck[];
  evaluated_at: string;
}

interface ResourceData {
  cpu: { model?: string; logical: number; effective: number; architecture: string };
  memory: { total_bytes: number; available_bytes: number; swap_total_bytes: number; swap_used_bytes: number };
  storage: { path: string; total_bytes: number; free_bytes: number };
  accelerators: Array<{ vendor: string; model?: string; total_vram_bytes?: number; used_vram_bytes?: number; temperature_c?: number }>;
  ollama: { status: string; endpoint: string; models: Array<{ name: string; compatibility: string; reason: string }> };
  health: { overall: string; ram: string; swap: string; disk: string; thermal: string; warnings?: string[] };
  recommendation: { concurrency: number; profile: string; reasons: string[]; recommended_model?: string };
}

function bytes(value: number): string {
  if (!value) return 'Unavailable';
  return `${(value / 1073741824).toFixed(1)} GiB`;
}

export function Operations() {
  const [data, setData] = useState<DoctorReportData | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [resources, setResources] = useState<ResourceData | null>(null);

  const fetchDoctor = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const [resp, resourceSnapshot] = await Promise.all([api.getDoctorReport(), api.getResources()]);
      setData(resp);
      setResources(resourceSnapshot);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to query system doctor report');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void fetchDoctor();
  }, [fetchDoctor]);

  return (
    <div className="operations-container">
      <div className="memory-header">
        <div className="memory-headline">
          <h2 className="memory-title">System Health & Doctor Diagnostics</h2>
          <span className="memory-subtitle">
            Subsystem verification for SQLite WAL, event bus, workers, providers, vector index, and sandbox
          </span>
        </div>
        <Button variant="secondary" size="sm" onClick={fetchDoctor}>
          Run Doctor Diagnostics
        </Button>
      </div>

      {loading ? (
        <LoadingState message="Running full diagnostic probe across DB, worker fleet, and model provider routes…" />
      ) : error ? (
        <ErrorState severity="error" message={error} onRetry={fetchDoctor} />
      ) : data ? (
        <div className="doctor-content">
          {/* Overall Health Card */}
          <div className="doctor-summary-card" style={{ marginBottom: 'var(--space-4)' }}>
            <div className="flex-row items-center justify-between">
              <div>
                <span className="text-xs text-dim">SYSTEM HEALTH STATUS</span>
                <h3 className="text-lg font-bold" style={{ marginTop: 'var(--space-1)' }}>
                  MARSHAL Core Engine: {data.overall_status}
                </h3>
              </div>
              <StatusBadge
                status={data.overall_status === 'READY' ? 'ready' : 'degraded'}
                label={data.overall_status}
              />
            </div>
          </div>

          {resources ? (
            <section className="doctor-summary-card" style={{ marginBottom: 'var(--space-4)' }} aria-label="Community resource awareness">
              <div className="flex-row items-center justify-between">
                <div>
                  <span className="text-xs text-dim">COMMUNITY RESOURCE AWARENESS</span>
                  <h3 className="text-lg font-bold" style={{ marginTop: 'var(--space-1)' }}>
                    Safe local concurrency: {resources.recommendation.concurrency}
                  </h3>
                </div>
                <StatusBadge status={resources.health.overall === 'OK' ? 'ready' : 'degraded'} label={resources.health.overall} />
              </div>
              <div className="grid-2" style={{ marginTop: 'var(--space-3)' }}>
                <div className="text-xs text-muted">CPU: {resources.cpu.model || 'Unavailable'} · {resources.cpu.effective}/{resources.cpu.logical} effective/logical</div>
                <div className="text-xs text-muted">RAM: {bytes(resources.memory.available_bytes)} available of {bytes(resources.memory.total_bytes)}</div>
                <div className="text-xs text-muted">Disk: {bytes(resources.storage.free_bytes)} free</div>
                <div className="text-xs text-muted">Ollama: {resources.ollama.status} · {resources.ollama.models.length} installed models</div>
              </div>
              {resources.recommendation.recommended_model ? <p className="text-xs text-muted" style={{ marginTop: 'var(--space-2)' }}>Recommended installed local model: {resources.recommendation.recommended_model}</p> : null}
              {resources.health.warnings?.length ? <p className="text-xs text-muted" style={{ marginTop: 'var(--space-2)' }}>Warnings: {resources.health.warnings.join('; ')}</p> : null}
            </section>
          ) : null}

          {/* Diagnostic Checks Table */}
          <div className="table-responsive" style={{ marginBottom: 'var(--space-5)' }}>
            <table className="data-table" aria-label="System Diagnostics Table">
              <thead>
                <tr>
                  <th>Component</th>
                  <th>Status</th>
                  <th>Latency</th>
                  <th>Diagnostic Details</th>
                </tr>
              </thead>
              <tbody>
                {data.checks.map((c) => (
                  <tr key={c.component}>
                    <td>
                      <code className="font-mono text-xs font-bold">{c.component}</code>
                    </td>
                    <td>
                      <StatusBadge
                        status={c.status === 'READY' ? 'ready' : 'degraded'}
                        label={c.status}
                      />
                    </td>
                    <td className="font-mono text-xs text-dim">{c.latency_ms} ms</td>
                    <td className="text-xs text-muted description-cell">{c.message}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          {/* State Backups & Restore Workspace */}
          <BackupRestoreWorkspace />

          {/* GC & Maintenance Workspace */}
          <MaintenanceWorkspace />

          {/* Release Trust & SBOM View */}
          <ReleaseTrustView />
        </div>
      ) : null}
    </div>
  );
}
