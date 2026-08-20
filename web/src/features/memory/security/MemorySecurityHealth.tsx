import { useState, useEffect, useCallback } from 'react';
import { api } from '../../../api/client';
import { StatusBadge, Button } from '../../../components/ui';
import { LoadingState, ErrorState } from '../../../components/state';

interface IndexHealth {
  name: string;
  generation: number;
  status: string;
  outbox_lag_ms: number;
  records_indexed: number;
}

interface ACLScopeSummary {
  scope: string;
  enforcement_mode: string;
  read_isolation: string;
  write_authority: string;
}

interface MemorySecurityHealthData {
  encryption_status: string;
  key_id: string;
  integrity_status: string;
  verified_records: number;
  tampered_records: number;
  rebuild_watermark: number;
  indexes: IndexHealth[];
  acl_matrix: ACLScopeSummary[];
  evaluated_at: string;
}

interface MemorySecurityHealthProps {
  onClose: () => void;
}

export function MemorySecurityHealth({ onClose }: MemorySecurityHealthProps) {
  const [data, setData] = useState<MemorySecurityHealthData | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const fetchHealth = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const resp = await api.getMemorySecurityHealth();
      setData(resp);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to query memory security health');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void fetchHealth();
  }, [fetchHealth]);

  return (
    <div className="modal-backdrop" onClick={onClose} role="dialog" aria-modal="true" aria-label="Memory Security Modal">
      <div className="modal-card modal-lg" onClick={(e) => e.stopPropagation()}>
        <div className="modal-header">
          <div className="task-detail-title-group">
            <h3 className="modal-title">Memory Security, Encryption, ACL & Index Health</h3>
            <span className="font-mono text-xs text-dim">Zero-Plaintext Security Invariant</span>
          </div>
          <button type="button" className="btn btn-ghost btn-sm" onClick={onClose} aria-label="Close">
            ✕
          </button>
        </div>

        <div className="modal-body">
          {loading ? (
            <LoadingState message="Verifying AES-256-GCM keys, index outbox lag, and signature watermarks…" />
          ) : error ? (
            <ErrorState severity="error" message={error} onRetry={fetchHealth} />
          ) : data ? (
            <div className="security-health-content">
              {/* Security Health Summary */}
              <div className="task-meta-grid" style={{ marginBottom: 'var(--space-3)' }}>
                <div className="meta-box">
                  <span className="meta-label">Encryption</span>
                  <div className="flex-row items-center gap-2">
                    <StatusBadge status="ready" label="AES-256-GCM" />
                    <code className="font-mono text-xs text-dim">{data.key_id}</code>
                  </div>
                </div>
                <div className="meta-box">
                  <span className="meta-label">Integrity Status</span>
                  <div className="flex-row items-center gap-2">
                    <StatusBadge
                      status={data.tampered_records === 0 ? 'ready' : 'degraded'}
                      label={data.integrity_status.toUpperCase()}
                    />
                    <span className="font-mono text-xs">
                      {data.verified_records} Verified / {data.tampered_records} Tampered
                    </span>
                  </div>
                </div>
                <div className="meta-box">
                  <span className="meta-label">Rebuild Watermark</span>
                  <span className="meta-value font-mono text-xs">{data.rebuild_watermark} Records</span>
                </div>
              </div>

              {/* Indexes Health Table */}
              <h4 className="font-semibold text-xs text-muted" style={{ marginBottom: 'var(--space-2)' }}>
                INDEX GENERATIONS & OUTBOX LATENCY
              </h4>
              <div className="table-responsive" style={{ marginBottom: 'var(--space-4)' }}>
                <table className="data-table" aria-label="Indexes Health Table">
                  <thead>
                    <tr>
                      <th>Index Name</th>
                      <th>Generation</th>
                      <th>Health Status</th>
                      <th>Outbox Lag (ms)</th>
                      <th>Records Indexed</th>
                    </tr>
                  </thead>
                  <tbody>
                    {data.indexes.map((idx) => (
                      <tr key={idx.name}>
                        <td>
                          <code className="font-mono text-xs">{idx.name}</code>
                        </td>
                        <td className="font-mono text-xs">gen-{idx.generation}</td>
                        <td>
                          <StatusBadge
                            status={idx.status === 'healthy' ? 'ready' : 'degraded'}
                            label={idx.status.toUpperCase()}
                          />
                        </td>
                        <td className="font-mono text-xs">
                          {idx.outbox_lag_ms} ms
                        </td>
                        <td className="font-mono text-xs">{idx.records_indexed}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>

              {/* ACL Matrix */}
              <h4 className="font-semibold text-xs text-muted" style={{ marginBottom: 'var(--space-2)' }}>
                ACL ISOLATION & WRITE AUTHORITY MATRIX
              </h4>
              <div className="table-responsive">
                <table className="data-table" aria-label="ACL Matrix Table">
                  <thead>
                    <tr>
                      <th>Scope</th>
                      <th>Enforcement Mode</th>
                      <th>Read Isolation</th>
                      <th>Write Authority</th>
                    </tr>
                  </thead>
                  <tbody>
                    {data.acl_matrix.map((acl) => (
                      <tr key={acl.scope}>
                        <td>
                          <code className="font-mono text-xs font-bold">{acl.scope}</code>
                        </td>
                        <td>
                          <span className="font-mono text-xs">{acl.enforcement_mode}</span>
                        </td>
                        <td className="text-xs text-dim">{acl.read_isolation}</td>
                        <td className="text-xs text-dim">{acl.write_authority}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
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
