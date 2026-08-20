import { useState, useEffect } from 'react';
import { api } from '../api/client';
import { StatusBadge, Button } from '../components/ui';
import { LoadingState, ErrorState } from '../components/state';

interface AgentDetailProps {
  agentId: string;
  onClose: () => void;
  onNavigateTask?: (taskId: string) => void;
}

interface AgentDetailData {
  id: string;
  name: string;
  provider: string;
  model: string;
  status: string;
  capabilities: string[];
  current_task_id?: string;
  current_run_id?: string;
  completed_task_count: number;
  failed_task_count: number;
  last_heartbeat: string;
  created_at: string;
  memory_contributions: {
    episodes_extracted: number;
    decisions_logged: number;
    facts_asserted: number;
  };
}

export function AgentDetail({ agentId, onClose, onNavigateTask }: AgentDetailProps) {
  const [agent, setAgent] = useState<AgentDetailData | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let active = true;
    setLoading(true);
    setError(null);

    api.getAgentDetail(agentId)
      .then((res) => {
        if (active) setAgent(res);
      })
      .catch((err: unknown) => {
        if (active) setError(err instanceof Error ? err.message : 'Failed to fetch agent details');
      })
      .finally(() => {
        if (active) setLoading(false);
      });

    return () => {
      active = false;
    };
  }, [agentId]);

  return (
    <div className="modal-backdrop" onClick={onClose} role="dialog" aria-modal="true" aria-label="Agent Detail">
      <div className="modal-card agent-detail-card" onClick={(e) => e.stopPropagation()}>
        <div className="modal-header">
          <div className="agent-detail-title-group">
            <h3 className="modal-title">{agent?.name ?? 'Agent Inspector'}</h3>
            <code className="agent-id-badge">{agentId}</code>
          </div>
          <button type="button" className="btn btn-ghost btn-sm" onClick={onClose} aria-label="Close">
            ✕
          </button>
        </div>

        <div className="modal-body">
          {loading && <LoadingState message="Inspecting agent runtime state…" />}
          {error && <ErrorState severity="error" message={error} />}
          {agent && (
            <div className="agent-detail-content">
              {/* Metadata Grid */}
              <div className="agent-meta-grid">
                <div className="meta-box">
                  <span className="meta-label">Status</span>
                  <StatusBadge status={agent.status === 'READY' ? 'ready' : 'degraded'} label={agent.status} />
                </div>
                <div className="meta-box">
                  <span className="meta-label">Provider / Model</span>
                  <span className="meta-value">{agent.provider.toUpperCase()} / <code>{agent.model}</code></span>
                </div>
                <div className="meta-box">
                  <span className="meta-label">Completed Tasks</span>
                  <span className="meta-value font-mono">{agent.completed_task_count}</span>
                </div>
                <div className="meta-box">
                  <span className="meta-label">Last Heartbeat</span>
                  <span className="meta-value font-mono text-xs">{new Date(agent.last_heartbeat).toLocaleTimeString()}</span>
                </div>
              </div>

              {/* Active Task Link */}
              {agent.current_task_id && (
                <div className="agent-active-task-banner">
                  <span>Currently assigned to task: </span>
                  <button
                    type="button"
                    className="btn btn-secondary btn-sm"
                    onClick={() => onNavigateTask?.(agent.current_task_id!)}
                  >
                    {agent.current_task_id} →
                  </button>
                </div>
              )}

              {/* Capabilities */}
              <div className="agent-detail-section">
                <h4 className="section-subtitle">Assigned Capabilities</h4>
                <div className="capability-tag-list">
                  {agent.capabilities.map((cap) => (
                    <span key={cap} className="capability-tag">
                      <code>{cap}</code>
                    </span>
                  ))}
                </div>
              </div>

              {/* Memory Contributions */}
              <div className="agent-detail-section">
                <h4 className="section-subtitle">Canonical Memory Contributions</h4>
                <div className="memory-contrib-grid">
                  <div className="contrib-item">
                    <span className="contrib-num">{agent.memory_contributions.episodes_extracted}</span>
                    <span className="contrib-label">Episodes Extracted</span>
                  </div>
                  <div className="contrib-item">
                    <span className="contrib-num">{agent.memory_contributions.decisions_logged}</span>
                    <span className="contrib-label">Decisions Logged</span>
                  </div>
                  <div className="contrib-item">
                    <span className="contrib-num">{agent.memory_contributions.facts_asserted}</span>
                    <span className="contrib-label">Facts Asserted</span>
                  </div>
                </div>
              </div>
            </div>
          )}
        </div>

        <div className="modal-actions">
          <Button variant="secondary" size="sm" onClick={onClose}>
            Close Inspector
          </Button>
        </div>
      </div>
    </div>
  );
}
