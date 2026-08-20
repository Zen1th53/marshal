import { useState, useEffect, useCallback } from 'react';
import { api } from '../api/client';
import { Button } from '../components/ui';
import { LoadingState, ErrorState } from '../components/state';

interface ProvenanceNode {
  id: string;
  type: string;
  title: string;
  producer: string;
  timestamp: string;
  relationship: string;
  is_proven_binding: boolean;
  parent_id?: string;
}

interface ProvenanceTraceData {
  target_id: string;
  root_node: ProvenanceNode;
  nodes: ProvenanceNode[];
  max_depth: number;
  total_nodes: number;
  generated_at: string;
}

export function Trace() {
  const [targetId, setTargetId] = useState('TASK-002-CONTROL-PLANE');
  const [data, setData] = useState<ProvenanceTraceData | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const fetchTrace = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const resp = await api.getProvenanceTrace(targetId);
      setData(resp);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to reconstruct causal provenance trace');
    } finally {
      setLoading(false);
    }
  }, [targetId]);

  useEffect(() => {
    void fetchTrace();
  }, [fetchTrace]);

  const getNodeIcon = (type: string) => {
    switch (type) {
      case 'task':
        return '📋';
      case 'memory_injection':
        return '🧠';
      case 'run':
        return '⚡';
      case 'evidence':
        return '🛡️';
      case 'review_decision':
        return '✍️';
      case 'audit_event':
        return '🔍';
      default:
        return '📦';
    }
  };

  return (
    <div className="trace-container">
      <div className="trace-header">
        <div className="trace-headline">
          <h2 className="trace-title">Causal Provenance & "Why" Trace</h2>
          <span className="trace-subtitle">Reconstruct full lifecycle lineage across Memory, Runs & Attestations</span>
        </div>
        <form
          className="trace-search-form"
          onSubmit={(e) => {
            e.preventDefault();
            void fetchTrace();
          }}
        >
          <input
            type="text"
            className="trace-input font-mono"
            value={targetId}
            onChange={(e) => setTargetId(e.target.value)}
            placeholder="Target Task or Run ID…"
            aria-label="Target ID"
          />
          <Button variant="secondary" size="sm" type="submit">
            Reconstruct Trace
          </Button>
        </form>
      </div>

      {loading ? (
        <LoadingState message="Reconstructing tamper-proof causal DAG and injected memory provenance…" />
      ) : error ? (
        <ErrorState severity="error" message={error} onRetry={fetchTrace} />
      ) : data ? (
        <div className="trace-tree-wrapper">
          <div className="trace-legend">
            <span className="legend-item">
              <span className="proven-lock">🔒</span> Cryptographically Proven Binding
            </span>
            <span className="legend-item">
              <span className="correlation-dash">🔗</span> Trace Correlation Match
            </span>
          </div>

          <div className="trace-timeline-lane">
            {data.nodes.map((node, index) => (
              <div key={node.id} className="trace-node-card">
                <div className="trace-connector">
                  <div className="connector-icon">{getNodeIcon(node.type)}</div>
                  {index < data.nodes.length - 1 && (
                    <div
                      className={`connector-line ${node.is_proven_binding ? 'line-proven' : 'line-dashed'}`}
                    />
                  )}
                </div>

                <div className="trace-node-body">
                  <div className="trace-node-header">
                    <div className="node-title-group">
                      <span className="node-type font-mono">{node.type.toUpperCase()}</span>
                      <h4 className="node-title">{node.title}</h4>
                    </div>
                    {node.is_proven_binding ? (
                      <span className="proven-badge" title="Cryptographically verified binding">
                        🔒 PROVEN BINDING
                      </span>
                    ) : (
                      <span className="correlation-badge" title="Correlated via Trace Context">
                        🔗 CORRELATED
                      </span>
                    )}
                  </div>

                  <div className="trace-node-meta font-mono text-xs">
                    <span>ID: <code>{node.id}</code></span>
                    <span>Producer: {node.producer}</span>
                    <span>Rel: {node.relationship}</span>
                    <span className="text-dim">{new Date(node.timestamp).toLocaleTimeString()}</span>
                  </div>
                </div>
              </div>
            ))}
          </div>
        </div>
      ) : null}
    </div>
  );
}
