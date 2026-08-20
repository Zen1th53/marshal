import { useState, useEffect, useCallback } from 'react';
import { api } from '../../../api/client';
import { StatusBadge, Button } from '../../../components/ui';
import { LoadingState, ErrorState } from '../../../components/state';

interface RetrievalCandidate {
  memory_id: string;
  title: string;
  kind: string;
  scope: string;
  lexical_rank: number;
  lexical_score: number;
  dense_rank: number;
  dense_score: number;
  graph_bonus: number;
  freshness_penalty: number;
  final_rrf_score: number;
  rerank_rationale: string;
}

interface RetrievalExplainData {
  query: string;
  embedder_model: string;
  embedder_status: string;
  fusion_algorithm: string;
  candidates: RetrievalCandidate[];
  evaluated_at: string;
}

interface RetrievalExplainabilityProps {
  query: string;
  onClose: () => void;
}

export function RetrievalExplainability({ query, onClose }: RetrievalExplainabilityProps) {
  const [data, setData] = useState<RetrievalExplainData | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const fetchExplain = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const resp = await api.explainRetrieval(query);
      setData(resp);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to query retrieval explainability');
    } finally {
      setLoading(false);
    }
  }, [query]);

  useEffect(() => {
    void fetchExplain();
  }, [fetchExplain]);

  return (
    <div className="modal-backdrop" onClick={onClose} role="dialog" aria-modal="true" aria-label="Retrieval Explainability Modal">
      <div className="modal-card run-detail-card" onClick={(e) => e.stopPropagation()}>
        <div className="modal-header">
          <div className="task-detail-title-group">
            <h3 className="modal-title">Hybrid Retrieval & RRF Fusion Explainability</h3>
            <span className="font-mono text-xs text-dim">Algorithm: RRF-k60</span>
          </div>
          <button type="button" className="btn btn-ghost btn-sm" onClick={onClose} aria-label="Close">
            ✕
          </button>
        </div>

        <div className="modal-body">
          {loading ? (
            <LoadingState message="Auditing lexical BM25 + dense vector ranking & RRF fusion weights…" />
          ) : error ? (
            <ErrorState severity="error" message={error} onRetry={fetchExplain} />
          ) : data ? (
            <div className="explainability-content">
              {/* Meta Header */}
              <div className="task-meta-grid" style={{ marginBottom: 'var(--space-3)' }}>
                <div className="meta-box">
                  <span className="meta-label">Query</span>
                  <span className="meta-value font-mono text-xs">"{data.query}"</span>
                </div>
                <div className="meta-box">
                  <span className="meta-label">Embedder Model</span>
                  <div className="flex-row items-center gap-2">
                    <code className="font-mono text-xs">{data.embedder_model}</code>
                    <StatusBadge
                      status={data.embedder_status === 'ready' ? 'ready' : 'degraded'}
                      label={data.embedder_status.toUpperCase()}
                    />
                  </div>
                </div>
                <div className="meta-box">
                  <span className="meta-label">Evaluated Candidates</span>
                  <span className="meta-value font-mono text-xs">{data.candidates.length}</span>
                </div>
              </div>

              {/* RRF Breakdown Table */}
              <div className="table-responsive">
                <table className="data-table" aria-label="RRF Candidates Breakdown Table">
                  <thead>
                    <tr>
                      <th>Rank</th>
                      <th>Candidate Title / ID</th>
                      <th>BM25 Lexical</th>
                      <th>Dense Vector</th>
                      <th>Graph / Age</th>
                      <th>Final RRF</th>
                      <th>Rerank Rationale</th>
                    </tr>
                  </thead>
                  <tbody>
                    {data.candidates.map((c, idx) => (
                      <tr key={c.memory_id}>
                        <td>
                          <span className="font-mono text-xs font-bold">#{idx + 1}</span>
                        </td>
                        <td>
                          <div className="candidate-cell">
                            <span className="font-semibold text-xs">{c.title}</span>
                            <code className="font-mono text-xs text-dim">{c.memory_id}</code>
                          </div>
                        </td>
                        <td className="font-mono text-xs">
                          r{c.lexical_rank} ({(c.lexical_score * 100).toFixed(0)}%)
                        </td>
                        <td className="font-mono text-xs">
                          r{c.dense_rank} ({(c.dense_score * 100).toFixed(0)}%)
                        </td>
                        <td className="font-mono text-xs">
                          +{c.graph_bonus} / {c.freshness_penalty}
                        </td>
                        <td>
                          <span className="rrf-score-badge font-mono text-xs">
                            {(c.final_rrf_score * 100).toFixed(0)}%
                          </span>
                        </td>
                        <td className="text-xs text-dim">{c.rerank_rationale}</td>
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
