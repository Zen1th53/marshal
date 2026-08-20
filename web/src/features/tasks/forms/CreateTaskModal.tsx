import { useState } from 'react';
import { api } from '../../../api/client';
import { Button } from '../../../components/ui';
import { useToast } from '../../../components/toast';

interface CreateTaskModalProps {
  isOpen: boolean;
  onClose: () => void;
  onSuccess: () => void;
}

export function CreateTaskModal({ isOpen, onClose, onSuccess }: CreateTaskModalProps) {
  const [title, setTitle] = useState('');
  const [description, setDescription] = useState('');
  const [risk, setRisk] = useState('LOW');
  const [assignedTo, setAssignedTo] = useState('agent-claude-planner');
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const { addToast } = useToast();

  if (!isOpen) return null;

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
          assigned_to: assignedTo,
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
      <div className="modal-card create-task-modal-card" onClick={(e) => e.stopPropagation()}>
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
                  <option value="LOW">Low Risk</option>
                  <option value="MEDIUM">Medium Risk</option>
                  <option value="HIGH">High Risk</option>
                  <option value="CRITICAL">Critical Risk</option>
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
                >
                  <option value="agent-claude-planner">Claude High-Reasoning Planner</option>
                  <option value="agent-codex-implementer">Codex Rapid Implementer</option>
                  <option value="agent-gemini-multimodal">Gemini 2.5 Pro Analyst</option>
                  <option value="agent-opencode-local">OpenCode Local Worker</option>
                </select>
              </div>
            </div>
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
