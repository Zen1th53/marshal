import { useState, useEffect, useCallback } from 'react';
import { api } from '../api/client';
import { StatusBadge, Button } from '../components/ui';
import { LoadingState, ErrorState, EmptyState } from '../components/state';

interface EvidenceItem {
  id: string;
  task_id: string;
  run_id: string;
  type: string;
  producer: string;
  digest: string;
  size_bytes: number;
  integrity_status: string;
  created_at: string;
}

interface EvidenceDetailData {
  id: string;
  task_id: string;
  run_id: string;
  type: string;
  producer: string;
  digest: string;
  calculated_digest: string;
  integrity_status: string;
  artifact_id?: string;
  signature: string;
  payload: Record<string, any>;
  created_at: string;
}

export function Evidence() {
  const [items, setItems] = useState<EvidenceItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [typeFilter, setTypeFilter] = useState('all');
  const [selectedEvidenceId, setSelectedEvidenceId] = useState<string | null>(null);
  const [detailData, setDetailData] = useState<EvidenceDetailData | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);

  const fetchEvidence = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const resp = await api.listEvidence({
        type: typeFilter !== 'all' ? typeFilter : undefined,
      });
      setItems(resp.items);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to load evidence records');
    } finally {
      setLoading(false);
    }
  }, [typeFilter]);

  useEffect(() => {
    void fetchEvidence();
  }, [fetchEvidence]);

  const handleInspect = async (id: string) => {
    setSelectedEvidenceId(id);
    setDetailLoading(true);
    try {
      const resp = await api.getEvidenceDetail(id);
      setDetailData(resp);
    } catch {
      // Handled in modal
    } finally {
      setDetailLoading(false);
    }
  };

  return (
    <div className="evidence-container">
      <div className="evidence-header">
        <div className="evidence-headline">
          <h2 className="evidence-title">Cryptographic Evidence & Proofs</h2>
          <span className="evidence-count font-mono text-xs">{items.length} Recorded Artifacts</span>
        </div>
        <Button variant="secondary" size="sm" onClick={fetchEvidence}>
          Refresh Evidence
        </Button>
      </div>

      {/* Filter Toolbar */}
      <div className="filter-toolbar">
        <div className="filter-group">
          <select
            className="filter-select"
            value={typeFilter}
            onChange={(e) => setTypeFilter(e.target.value)}
            aria-label="Filter by evidence type"
          >
            <option value="all">All Evidence Types</option>
            <option value="test_execution">Test Execution</option>
            <option value="merkle_proof">Merkle Attestation Proof</option>
            <option value="benchmark_report">Benchmark Report</option>
            <option value="security_attestation">Security Attestation</option>
          </select>
        </div>
      </div>

      {/* Table Content */}
      {loading ? (
        <LoadingState message="Querying tamper-evident integrity proof registry…" />
      ) : error ? (
        <ErrorState severity="error" message={error} onRetry={fetchEvidence} />
      ) : items.length === 0 ? (
        <EmptyState
          title="No evidence items found"
          description="There are no cryptographic evidence records matching your current filter criteria."
        />
      ) : (
        <div className="table-responsive">
          <table className="data-table" aria-label="Evidence Records Table">
            <thead>
              <tr>
                <th>Evidence ID</th>
                <th>Task / Run Context</th>
                <th>Type</th>
                <th>Producer Agent</th>
                <th>SHA-256 Digest</th>
                <th>Integrity</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {items.map((item) => (
                <tr key={item.id}>
                  <td>
                    <code className="evidence-id-code">{item.id}</code>
                  </td>
                  <td>
                    <div className="evidence-context-cell">
                      <code className="text-xs">{item.task_id}</code>
                      <span className="text-dim text-xs font-mono">{item.run_id}</span>
                    </div>
                  </td>
                  <td>
                    <span className="evidence-type-badge">{item.type.replace('_', ' ').toUpperCase()}</span>
                  </td>
                  <td>
                    <span className="font-mono text-xs">{item.producer}</span>
                  </td>
                  <td>
                    <code className="font-mono text-xs text-dim">{item.digest.slice(0, 16)}…</code>
                  </td>
                  <td>
                    <StatusBadge
                      status={item.integrity_status === 'verified' ? 'ready' : 'degraded'}
                      label={item.integrity_status.toUpperCase()}
                    />
                  </td>
                  <td>
                    <Button variant="secondary" size="sm" onClick={() => handleInspect(item.id)}>
                      Inspect Proof
                    </Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {/* Evidence Detail Modal */}
      {selectedEvidenceId && (
        <div className="modal-backdrop" onClick={() => setSelectedEvidenceId(null)} role="dialog" aria-modal="true">
          <div className="modal-card" onClick={(e) => e.stopPropagation()}>
            <div className="modal-header">
              <div className="task-detail-title-group">
                <h3 className="modal-title">Integrity Proof Details</h3>
                <code className="task-id-badge">{selectedEvidenceId}</code>
              </div>
              <button
                type="button"
                className="btn btn-ghost btn-sm"
                onClick={() => setSelectedEvidenceId(null)}
                aria-label="Close"
              >
                ✕
              </button>
            </div>

            <div className="modal-body">
              {detailLoading ? (
                <LoadingState message="Verifying cryptographic digest & Ed25519 signature…" />
              ) : detailData ? (
                <div className="evidence-detail-content">
                  <div className="task-meta-grid">
                    <div className="meta-box">
                      <span className="meta-label">Integrity</span>
                      <StatusBadge
                        status={detailData.integrity_status === 'verified' ? 'ready' : 'degraded'}
                        label={detailData.integrity_status.toUpperCase()}
                      />
                    </div>
                    <div className="meta-box">
                      <span className="meta-label">Producer</span>
                      <span className="meta-value font-mono text-xs">{detailData.producer}</span>
                    </div>
                  </div>

                  <div className="meta-box" style={{ marginTop: 'var(--space-2)' }}>
                    <span className="meta-label">SHA-256 Digest (Verified):</span>
                    <code className="font-mono text-xs" style={{ wordBreak: 'break-all' }}>
                      {detailData.digest}
                    </code>
                  </div>

                  <div className="meta-box" style={{ marginTop: 'var(--space-2)' }}>
                    <span className="meta-label">Ed25519 Signature Proof:</span>
                    <code className="font-mono text-xs text-dim" style={{ wordBreak: 'break-all' }}>
                      {detailData.signature}
                    </code>
                  </div>

                  <div className="evidence-payload-box" style={{ marginTop: 'var(--space-3)' }}>
                    <h4 className="section-subtitle">Attestation Payload</h4>
                    <pre className="evidence-json-viewer">
                      {JSON.stringify(detailData.payload, null, 2)}
                    </pre>
                  </div>
                </div>
              ) : null}
            </div>

            <div className="modal-actions">
              {detailData?.artifact_id && (
                <a
                  href={`/api/v1/artifacts/${detailData.artifact_id}/download`}
                  className="btn btn-secondary btn-sm"
                  download
                >
                  Download Evidence Artifact
                </a>
              )}
              <Button variant="ghost" size="sm" onClick={() => setSelectedEvidenceId(null)}>
                Close
              </Button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
