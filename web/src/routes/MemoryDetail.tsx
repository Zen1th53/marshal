import { useState, useEffect, useCallback } from 'react';
import { api } from '../api/client';
import { StatusBadge, Button } from '../components/ui';
import { CorrelationLink } from '../components/audit/CorrelationLink';
import { LoadingState, ErrorState } from '../components/state';

interface MemoryDetailProps {
  memoryId: string;
  onClose: () => void;
}

interface MemoryProvenance {
  producer_agent_id: string;
  source_run_id: string;
  correlation_id: string;
  evidence_ids: string[];
  created_at: string;
}

interface MemoryLineage {
  supersedes_id?: string;
  superseded_by_id?: string;
  conflict_status: string;
  lineage_depth: number;
}

interface MemoryDetailData {
  id: string;
  project_id: string;
  scope: string;
  scope_id: string;
  kind: string;
  title: string;
  body: string;
  lifecycle: string;
  authority: string;
  confidence: number;
  digest_sha256: string;
  revision: number;
  is_encrypted: boolean;
  observed_at: string;
  expires_at?: string;
  provenance: MemoryProvenance;
  lineage: MemoryLineage;
}

export function MemoryDetail({ memoryId, onClose }: MemoryDetailProps) {
  const [data, setData] = useState<MemoryDetailData | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const fetchDetail = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const resp = await api.getMemoryDetail(memoryId);
      setData(resp);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to query memory provenance and detail');
    } finally {
      setLoading(false);
    }
  }, [memoryId]);

  useEffect(() => {
    void fetchDetail();
  }, [fetchDetail]);

  return (
    <div className="modal-backdrop" onClick={onClose} role="dialog" aria-modal="true" aria-label="Memory Record Inspector">
      <div className="modal-card" onClick={(e) => e.stopPropagation()}>
        <div className="modal-header">
          <div className="task-detail-title-group">
            <h3 className="modal-title">{data?.title || 'Memory Record Inspector'}</h3>
            <code className="task-id-badge">{memoryId}</code>
          </div>
          <button type="button" className="btn btn-ghost btn-sm" onClick={onClose} aria-label="Close">
            ✕
          </button>
        </div>

        <div className="modal-body">
          {loading ? (
            <LoadingState message="Inspecting memory node, cryptographic digest, and causal provenance…" />
          ) : error ? (
            <ErrorState severity="error" message={error} onRetry={fetchDetail} />
          ) : data ? (
            <div className="memory-detail-content">
              {/* Meta Grid */}
              <div className="task-meta-grid" style={{ marginBottom: 'var(--space-3)' }}>
                <div className="meta-box">
                  <span className="meta-label">Kind</span>
                  <span className="meta-value font-mono text-xs">{data.kind.toUpperCase()}</span>
                </div>
                <div className="meta-box">
                  <span className="meta-label">Scope & Scope ID</span>
                  <span className="meta-value font-mono text-xs">
                    {data.scope} ({data.scope_id})
                  </span>
                </div>
                <div className="meta-box">
                  <span className="meta-label">Revision & Digest</span>
                  <code className="meta-value font-mono text-xs">
                    r{data.revision} • {data.digest_sha256.slice(0, 10)}…
                  </code>
                </div>
                <div className="meta-box">
                  <span className="meta-label">Authority</span>
                  <StatusBadge
                    status={data.authority === 'verified' ? 'ready' : 'degraded'}
                    label={data.authority.toUpperCase()}
                  />
                </div>
              </div>

              {/* Provenance Card */}
              <div className="meta-section-box" style={{ marginBottom: 'var(--space-3)' }}>
                <h5 className="section-subtitle">Causal Provenance & Attestation</h5>
                <div className="provenance-grid font-mono text-xs">
                  <div>
                    <span className="text-dim">Producer:</span> <code>{data.provenance.producer_agent_id}</code>
                  </div>
                  <div>
                    <span className="text-dim">Source Run:</span> <code>{data.provenance.source_run_id}</code>
                  </div>
                  <div>
                    <span className="text-dim">Trace:</span> <CorrelationLink correlationId={data.provenance.correlation_id} />
                  </div>
                  <div>
                    <span className="text-dim">Evidence IDs:</span>{' '}
                    {data.provenance.evidence_ids.map((eid) => (
                      <code key={eid} className="evidence-chip font-mono">{eid}</code>
                    ))}
                  </div>
                </div>
              </div>

              {/* Temporal & Lineage */}
              <div className="task-meta-grid" style={{ marginBottom: 'var(--space-3)' }}>
                <div className="meta-box">
                  <span className="meta-label">Temporal State</span>
                  <span className="meta-value font-mono text-xs">
                    {data.expires_at ? `Expires: ${new Date(data.expires_at).toLocaleDateString()}` : 'PERMANENT (No TTL)'}
                  </span>
                </div>
                <div className="meta-box">
                  <span className="meta-label">Lineage & Conflicts</span>
                  <span className="meta-value font-mono text-xs">
                    Conflict: {data.lineage.conflict_status.toUpperCase()} (Depth: {data.lineage.lineage_depth})
                  </span>
                </div>
              </div>

              {/* Body Content */}
              <div className="memory-modal-section">
                <h5 className="section-subtitle">Record Body Content</h5>
                <div className="code-block font-mono text-xs" style={{ maxHeight: '200px', overflowY: 'auto' }}>
                  {data.body}
                </div>
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
