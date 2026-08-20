import { useState, useEffect, useCallback } from 'react';
import { api } from '../../api/client';
import { StatusBadge, Button } from '../../components/ui';
import { LoadingState, ErrorState } from '../../components/state';
import type { TaskStatus } from '../../api/types';

interface DAGNode {
  id: string;
  title: string;
  status: TaskStatus;
  risk: string;
  assigned_to?: string;
  layer: number;
}

interface DAGEdge {
  source_id: string;
  target_id: string;
  type: string;
}

interface DAGData {
  nodes: DAGNode[];
  edges: DAGEdge[];
  has_cycles: boolean;
  cycle_path?: string[];
  max_depth: number;
}

interface DAGViewerProps {
  onSelectTask?: (taskId: string) => void;
}

export function DAGViewer({ onSelectTask }: DAGViewerProps) {
  const [data, setData] = useState<DAGData | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [selectedNodeId, setSelectedNodeId] = useState<string | null>(null);
  const [maxDepth, setMaxDepth] = useState(5);

  const fetchDAG = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const resp = await api.getTaskDAG(maxDepth);
      setData(resp);
      setSelectedNodeId((prev) => prev ?? (resp.nodes.length > 0 ? resp.nodes[0].id : null));
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to load task DAG');
    } finally {
      setLoading(false);
    }
  }, [maxDepth]);

  useEffect(() => {
    void fetchDAG();
  }, [fetchDAG]);

  // Group nodes by deterministic topological layer
  const layersMap = new Map<number, DAGNode[]>();
  if (data) {
    for (const node of data.nodes) {
      if (!layersMap.has(node.layer)) {
        layersMap.set(node.layer, []);
      }
      layersMap.get(node.layer)!.push(node);
    }
  }

  const sortedLayers = Array.from(layersMap.keys()).sort((a, b) => a - b);

  return (
    <div className="dag-viewer-container">
      <div className="dag-toolbar">
        <div className="dag-toolbar-left">
          <h3 className="dag-toolbar-title">Deterministic Task DAG</h3>
          <span className="dag-node-count">{data?.nodes.length ?? 0} Nodes • {data?.edges.length ?? 0} Edges</span>
        </div>

        <div className="dag-toolbar-right">
          <label className="dag-depth-label">
            Max Depth:
            <select
              className="filter-select"
              value={maxDepth}
              onChange={(e) => setMaxDepth(Number(e.target.value))}
              aria-label="DAG Depth"
            >
              {[1, 2, 3, 5, 8, 10].map((d) => (
                <option key={d} value={d}>{d}</option>
              ))}
            </select>
          </label>
          <Button variant="secondary" size="sm" onClick={fetchDAG}>
            Recalculate Layout
          </Button>
        </div>
      </div>

      {data?.has_cycles && (
        <div className="dag-cycle-alert" role="alert">
          <strong>⚠️ Cycle Detected in Task Graph:</strong>{' '}
          <code>{data.cycle_path?.join(' → ')}</code>
        </div>
      )}

      {loading && !data ? (
        <LoadingState message="Computing deterministic DAG layout…" />
      ) : error ? (
        <ErrorState severity="error" message={error} onRetry={fetchDAG} />
      ) : (
        <div className="dag-viewport" role="region" aria-label="Interactive Task DAG Viewport">
          <div className="dag-layers-flow">
            {sortedLayers.map((layerIdx) => {
              const layerNodes = layersMap.get(layerIdx)!;
              return (
                <div key={layerIdx} className="dag-layer-column">
                  <div className="dag-layer-header">
                    <span>Layer {layerIdx}</span>
                  </div>
                  <div className="dag-layer-nodes">
                    {layerNodes.map((node) => {
                      const isSelected = selectedNodeId === node.id;
                      return (
                        <div
                          key={node.id}
                          className={`dag-node-card ${isSelected ? 'selected' : ''}`}
                          onClick={() => {
                            setSelectedNodeId(node.id);
                            onSelectTask?.(node.id);
                          }}
                          role="button"
                          tabIndex={0}
                          aria-selected={isSelected}
                          onKeyDown={(e) => {
                            if (e.key === 'Enter' || e.key === ' ') {
                              e.preventDefault();
                              setSelectedNodeId(node.id);
                              onSelectTask?.(node.id);
                            }
                          }}
                        >
                          <div className="dag-node-header">
                            <code className="dag-node-id">{node.id}</code>
                            <span className={`risk-badge risk-${node.risk.toLowerCase()}`}>
                              {node.risk}
                            </span>
                          </div>
                          <p className="dag-node-title">{node.title}</p>
                          <div className="dag-node-footer">
                            <StatusBadge
                              status={node.status === 'completed' || node.status === 'running' ? 'ready' : 'degraded'}
                              label={node.status.toUpperCase()}
                            />
                            {node.assigned_to && (
                              <span className="dag-node-agent font-mono text-xs">
                                {node.assigned_to}
                              </span>
                            )}
                          </div>
                        </div>
                      );
                    })}
                  </div>
                </div>
              );
            })}
          </div>
        </div>
      )}
    </div>
  );
}
