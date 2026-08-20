import { useState, useEffect, useCallback } from 'react';
import { api } from '../api/client';
import { StatusBadge, Button } from '../components/ui';
import { LoadingState, ErrorState } from '../components/state';

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

export function Operations() {
  const [data, setData] = useState<DoctorReportData | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const fetchDoctor = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const resp = await api.getDoctorReport();
      setData(resp);
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

          {/* Diagnostic Checks Table */}
          <div className="table-responsive">
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
        </div>
      ) : null}
    </div>
  );
}
