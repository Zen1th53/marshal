import { useState, useEffect } from 'react';
import { api } from '../../../api/client';
import { Button } from '../../../components/ui';
import { useToast } from '../../../components/toast';
import type { AgentSummaryDTO, TaskSummaryDTO } from '../../../api/types';

interface CreateTaskModalProps {
  isOpen: boolean;
  onClose: () => void;
  onSuccess: () => void;
}

const RISK_LEVELS = [
  { value: 'R0', label: 'R0 — Read-Only & Inspection' },
  { value: 'R1', label: 'R1 — Sandboxed Local Development' },
  { value: 'R2', label: 'R2 — Verified Quorum Required' },
  { value: 'R3', label: 'R3 — Critical Architecture & Security' },
];

export function CreateTaskModal({ isOpen, onClose, onSuccess }: CreateTaskModalProps) {
  const [title, setTitle] = useState('');
  const [description, setDescription] = useState('');
  const [risk, setRisk] = useState('R1');
  const [assignedTo, setAssignedTo] = useState('');
  const [selectedDependencies, setSelectedDependencies] = useState<string[]>([]);
  const [agents, setAgents] = useState<AgentSummaryDTO[]>([]);
  const [existingTasks, setExistingTasks] = useState<TaskSummaryDTO[]>([]);
  const [loadingData, setLoadingData] = useState(true);
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const { addToast } = useToast();

  useEffect(() => {
    if (!isOpen) return;
    let active = true;
    setLoadingData(true);

    Promise.all([
      api.getAgents().catch(() => ({ items: [] as AgentSummaryDTO[] })),
      api.getTasks().catch(() => ({ items: [] as TaskSummaryDTO[] })),
    ]).then(([agentsRes, tasksRes]) => {
      if (!active) return;
      const loadedAgents = agentsRes.items ?? [];
      setAgents(loadedAgents);
      if (loadedAgents.length > 0 && !assignedTo) {
        setAssignedTo(loadedAgents[0].id);
      }
      setExistingTasks(tasksRes.items ?? []);
    }).finally(() => {
      if (active) setLoadingData(false);
    });

    return () => {
      active = false;
    };
  }, [isOpen]);

  if (!isOpen) return null;

  const toggleDependency = (taskId: string) => {
    setSelectedDependencies((prev) =>
      prev.includes(taskId) ? prev.filter((id) => id !== taskId) : [...prev, taskId]
    );
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!title.trim()) {
      setError('Task title is required');
      return;
    }

    setIsSubmitting(true);
    setError(null);

    const idempotencyKey = `task-create-${Date.now()}-${Math.random().toString(36).slice(2, 9)}`;

    try {
      await api.createTask(
        {
          title: title.trim(),
          description: description.trim(),
          risk,
          assigned_to: assignedTo || undefined,
          dependencies: selectedDependencies.length > 0 ? selectedDependencies : undefined,
        },
        idempotencyKey
      );

      addToast({
        type: 'success',
        message: `Task "${title}" created successfully.`,
      });

      onSuccess();
      onClose();
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to create task');
    } finally {
      setIsSubmitting(false);
    }
  };

  return (
    <div className="modal-backdrop" onClick={onClose} role="dialog" aria-modal="true" aria-label="Create Task">
      <div className="modal-card create-task-modal-card" onClick={(e) => e.stopPropagation()} style={{ maxWidth: '620px' }}>
        <div className="modal-header">
          <h3 className="modal-title">Create Autonomous Task</h3>
          <button type="button" className="btn btn-ghost btn-sm" onClick={onClose} aria-label="Close">
            ✕
          </button>
        </div>

        <form onSubmit={handleSubmit}>
          <div className="modal-body">
            {error && (
              <div className="alert-banner alert-error" role="alert">
                {error}
              </div>
            )}

            <div className="form-group">
              <label htmlFor="task-title" className="form-label">
                Task Title <span className="required-star">*</span>
              </label>
              <input
                id="task-title"
                type="text"
                className="form-input"
                placeholder="e.g. Implement Multi-Hop Memory Retrieval"
                value={title}
                onChange={(e) => setTitle(e.target.value)}
                required
                autoFocus
              />
            </div>

            <div className="form-group">
              <label htmlFor="task-desc" className="form-label">
                Objective & Plan Description
              </label>
              <textarea
                id="task-desc"
                className="form-textarea"
                rows={3}
                placeholder="Specify execution goals, invariants, and acceptance criteria…"
                value={description}
                onChange={(e) => setDescription(e.target.value)}
              />
            </div>

            <div className="form-row-two">
              <div className="form-group">
                <label htmlFor="task-risk" className="form-label">
                  Risk Level
                </label>
                <select
                  id="task-risk"
                  className="form-select"
                  value={risk}
                  onChange={(e) => setRisk(e.target.value)}
                >
                  {RISK_LEVELS.map((r) => (
                    <option key={r.value} value={r.value}>
                      {r.label}
                    </option>
                  ))}
                </select>
              </div>

              <div className="form-group">
                <label htmlFor="task-agent" className="form-label">
                  Assigned Agent
                </label>
                <select
                  id="task-agent"
                  className="form-select"
                  value={assignedTo}
                  onChange={(e) => setAssignedTo(e.target.value)}
                  disabled={loadingData}
                >
                  {agents.length === 0 ? (
                    <option value="">{loadingData ? 'Loading agent fleet…' : 'No agents registered'}</option>
                  ) : (
                    agents.map((a) => (
                      <option key={a.id} value={a.id}>
                        {a.name} ({a.provider?.toUpperCase() ?? 'GENERIC'} • {a.role ?? 'developer'})
                      </option>
                    ))
                  )}
                </select>
              </div>
            </div>

            {/* Task Dependencies */}
            {existingTasks.length > 0 && (
              <div className="form-group" style={{ marginTop: '0.75rem' }}>
                <label className="form-label">Task Dependencies (DAG Prerequisite Tasks)</label>
                <div style={{ display: 'flex', flexWrap: 'wrap', gap: '0.375rem', maxHeight: '100px', overflowY: 'auto', padding: '0.5rem', background: 'var(--color-surface, #1e293b)', borderRadius: '6px' }}>
                  {existingTasks.map((t) => {
                    const isSelected = selectedDependencies.includes(t.id);
                    return (
                      <button
                        key={t.id}
                        type="button"
                        className={`cap-pill ${isSelected ? 'active' : ''}`}
                        onClick={() => toggleDependency(t.id)}
                        style={{
                          cursor: 'pointer',
                          fontSize: '0.75rem',
                          background: isSelected ? 'var(--color-primary, #3b82f6)' : 'rgba(255,255,255,0.05)',
                          color: '#ffffff',
                          border: '1px solid var(--color-border, #334155)',
                        }}
                      >
                        {isSelected ? `✓ ${t.id}` : `+ ${t.id}`}
                      </button>
                    );
                  })}
                </div>
              </div>
            )}
          </div>

          <div className="modal-actions">
            <Button variant="secondary" size="sm" onClick={onClose} type="button" disabled={isSubmitting}>
              Cancel
            </Button>
            <Button variant="primary" size="sm" type="submit" disabled={isSubmitting || !title.trim()}>
              {isSubmitting ? 'Creating…' : 'Create Task'}
            </Button>
          </div>
        </form>
      </div>
    </div>
  );
}
