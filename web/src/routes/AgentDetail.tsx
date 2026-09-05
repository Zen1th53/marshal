import { useState, useEffect } from 'react';
import { api } from '../api/client';
import { StatusBadge, Button } from '../components/ui';
import { LoadingState, ErrorState } from '../components/state';
import { EditAgentModal } from '../features/agents/forms/EditAgentModal';
import type { AgentDetailDTO } from '../api/types';

interface AgentDetailProps {
  agentId: string;
  onClose: () => void;
  onNavigateTask?: (taskId: string) => void;
  onAgentMutated?: () => void;
}

export function AgentDetail({ agentId, onClose, onNavigateTask, onAgentMutated }: AgentDetailProps) {
  const [agent, setAgent] = useState<AgentDetailDTO | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [isEditing, setIsEditing] = useState(false);
  const [actionLoading, setActionLoading] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);

  const fetchDetail = () => {
    setLoading(true);
    setError(null);
    api.getAgentDetail(agentId)
      .then((res) => {
        setAgent(res);
      })
      .catch((err: unknown) => {
        setError(err instanceof Error ? err.message : 'Failed to fetch agent details');
      })
      .finally(() => {
        setLoading(false);
      });
  };

  useEffect(() => {
    fetchDetail();
  }, [agentId]);

  const handleToggleStatus = async () => {
    if (!agent) return;
    setActionLoading(true);
    setError(null);
    const newStatus = agent.status === 'disabled' ? 'active' : 'disabled';
    try {
      const updated = await api.updateAgent(
        agent.id,
        { status: newStatus },
        agent.revision
      );
      setAgent(updated);
      onAgentMutated?.();
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to update agent status');
    } finally {
      setActionLoading(false);
    }
  };

  const handleDelete = async () => {
    if (!agent) return;
    setActionLoading(true);
    setError(null);
    try {
      await api.deleteAgent(agent.id, agent.revision);
      onAgentMutated?.();
      onClose();
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to unregister agent');
      setActionLoading(false);
    }
  };

  const handleUpdated = (updated: AgentDetailDTO) => {
    setAgent(updated);
    onAgentMutated?.();
  };

  return (
    <div className="modal-backdrop" onClick={onClose} role="dialog" aria-modal="true" aria-label="Agent Detail">
      <div className="modal-card agent-detail-card" onClick={(e) => e.stopPropagation()} style={{ maxWidth: '640px' }}>
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
                  <StatusBadge status={agent.status === 'disabled' ? 'degraded' : 'ready'} label={agent.status.toUpperCase()} />
                </div>
                <div className="meta-box">
                  <span className="meta-label">Role</span>
                  <span className="meta-value font-mono">{(agent.role || 'developer').toUpperCase()}</span>
                </div>
                <div className="meta-box">
                  <span className="meta-label">Provider / Model</span>
                  <span className="meta-value">{(agent.provider || 'GENERIC').toUpperCase()} / <code>{agent.model || 'default'}</code></span>
                </div>
                <div className="meta-box">
                  <span className="meta-label">Revision</span>
                  <span className="meta-value font-mono">r{agent.revision ?? 0}</span>
                </div>
                <div className="meta-box">
                  <span className="meta-label">Completed Tasks</span>
                  <span className="meta-value font-mono">{agent.completed_task_count ?? 0}</span>
                </div>
                <div className="meta-box">
                  <span className="meta-label">Last Heartbeat</span>
                  <span className="meta-value font-mono text-xs">
                    {agent.last_heartbeat ? new Date(agent.last_heartbeat).toLocaleTimeString() : 'N/A'}
                  </span>
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
                  {(agent.capabilities ?? []).map((cap) => (
                    <span key={cap} className="capability-tag">
                      <code>{cap}</code>
                    </span>
                  ))}
                  {(!agent.capabilities || agent.capabilities.length === 0) && (
                    <span style={{ color: 'var(--color-text-muted, #94a3b8)', fontSize: '0.875rem' }}>No capabilities assigned</span>
                  )}
                </div>
              </div>

              {/* Memory Contributions */}
              <div className="agent-detail-section">
                <h4 className="section-subtitle">Canonical Memory Contributions</h4>
                <div className="memory-contrib-grid">
                  <div className="contrib-item">
                    <span className="contrib-num">{agent.memory_contributions?.episodes_extracted ?? 0}</span>
                    <span className="contrib-label">Episodes Extracted</span>
                  </div>
                  <div className="contrib-item">
                    <span className="contrib-num">{agent.memory_contributions?.decisions_logged ?? 0}</span>
                    <span className="contrib-label">Decisions Logged</span>
                  </div>
                  <div className="contrib-item">
                    <span className="contrib-num">{agent.memory_contributions?.facts_asserted ?? 0}</span>
                    <span className="contrib-label">Facts Asserted</span>
                  </div>
                </div>
              </div>
            </div>
          )}
        </div>

        <div className="modal-actions" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
          <div>
            {confirmDelete ? (
              <div style={{ display: 'flex', gap: '0.5rem', alignItems: 'center' }}>
                <span style={{ fontSize: '0.875rem', color: 'var(--color-danger, #ef4444)' }}>Confirm deletion?</span>
                <Button variant="danger" size="sm" onClick={handleDelete} disabled={actionLoading}>
                  {actionLoading ? 'Deleting…' : 'Yes, Delete'}
                </Button>
                <Button variant="secondary" size="sm" onClick={() => setConfirmDelete(false)}>
                  Cancel
                </Button>
              </div>
            ) : (
              <Button variant="ghost" size="sm" onClick={() => setConfirmDelete(true)} disabled={actionLoading || !agent}>
                Unregister Agent
              </Button>
            )}
          </div>

          <div style={{ display: 'flex', gap: '0.5rem' }}>
            {agent && (
              <>
                <Button
                  variant="secondary"
                  size="sm"
                  onClick={handleToggleStatus}
                  disabled={actionLoading}
                >
                  {agent.status === 'disabled' ? 'Enable Agent' : 'Disable Agent'}
                </Button>
                <Button
                  variant="secondary"
                  size="sm"
                  onClick={() => setIsEditing(true)}
                  disabled={actionLoading}
                >
                  Edit Configuration
                </Button>
              </>
            )}
            <Button variant="primary" size="sm" onClick={onClose}>
              Close
            </Button>
          </div>
        </div>
      </div>

      {isEditing && agent && (
        <EditAgentModal
          agent={agent}
          isOpen={isEditing}
          onClose={() => setIsEditing(false)}
          onUpdated={handleUpdated}
        />
      )}
    </div>
  );
}
