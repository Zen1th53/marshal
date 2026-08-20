import { useState, useEffect } from 'react';
import { api } from '../api/client';
import { StatusBadge, Button } from '../components/ui';
import { LoadingState, ErrorState } from '../components/state';
import { CorrelationLink } from '../components/audit/CorrelationLink';
import type { TaskStatus } from '../api/types';

interface TaskDetailProps {
  taskId: string;
  onClose: () => void;
  onNavigateRuns?: (taskId: string) => void;
}

interface TaskComprehensiveData {
  id: string;
  title: string;
  description: string;
  status: TaskStatus;
  risk: string;
  assigned_to?: string;
  base_commit: string;
  head_commit: string;
  head_mismatch_detected: boolean;
  approvals_count: number;
  required_quorum: number;
  stale_approval_detected: boolean;
  correlation_id: string;
  created_at: string;
  updated_at: string;
  lifecycle_history: Array<{
    timestamp: string;
    actor: string;
    state: string;
    message: string;
  }>;
  runs: Array<{
    run_id: string;
    status: string;
    step_count: number;
    duration_ms: number;
    started_at: string;
  }>;
}

export function TaskDetail({ taskId, onClose, onNavigateRuns }: TaskDetailProps) {
  const [task, setTask] = useState<TaskComprehensiveData | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let active = true;
    setLoading(true);
    setError(null);

    // Fetch comprehensive task detail
    api.getTaskDetail(taskId)
      .then((res) => {
        if (active) setTask(res as unknown as TaskComprehensiveData);
      })
      .catch((err: unknown) => {
        if (active) setError(err instanceof Error ? err.message : 'Failed to fetch task details');
      })
      .finally(() => {
        if (active) setLoading(false);
      });

    return () => {
      active = false;
    };
  }, [taskId]);

  return (
    <div className="modal-backdrop" onClick={onClose} role="dialog" aria-modal="true" aria-label="Task Detail Inspector">
      <div className="modal-card task-detail-card" onClick={(e) => e.stopPropagation()}>
        <div className="modal-header">
          <div className="task-detail-title-group">
            <h3 className="modal-title">{task?.title ?? 'Task Inspector'}</h3>
            <code className="task-id-badge">{taskId}</code>
          </div>
          <button type="button" className="btn btn-ghost btn-sm" onClick={onClose} aria-label="Close">
            ✕
          </button>
        </div>

        <div className="modal-body">
          {loading && <LoadingState message="Querying task timeline and verification state…" />}
          {error && <ErrorState severity="error" message={error} />}
          {task && (
            <div className="task-detail-content">
              {/* Warnings */}
              {task.stale_approval_detected && (
                <div className="alert-banner alert-warning" role="alert">
                  ⚠️ Stale Approvals Detected: Commits modified after quorum signature. Re-approval required.
                </div>
              )}
              {task.head_mismatch_detected && (
                <div className="alert-banner alert-error" role="alert">
                  ⛔ Git Head Mismatch: Current branch head differs from task baseline commit.
                </div>
              )}

              {/* Status and Risk Grid */}
              <div className="task-meta-grid">
                <div className="meta-box">
                  <span className="meta-label">Status</span>
                  <StatusBadge
                    status={task.status === 'completed' || task.status === 'running' ? 'ready' : 'degraded'}
                    label={task.status.toUpperCase()}
                  />
                </div>
                <div className="meta-box">
                  <span className="meta-label">Risk Level</span>
                  <span className={`risk-badge risk-${task.risk.toLowerCase()}`}>{task.risk}</span>
                </div>
                <div className="meta-box">
                  <span className="meta-label">Assigned Agent</span>
                  <span className="meta-value font-mono text-xs">{task.assigned_to ?? 'Unassigned'}</span>
                </div>
                <div className="meta-box">
                  <span className="meta-label">Quorum Approvals</span>
                  <span className="meta-value font-mono">{task.approvals_count} / {task.required_quorum}</span>
                </div>
              </div>

              {/* Commits & Correlation */}
              <div className="task-commits-strip">
                <span className="meta-label">Git Commits:</span>
                <code>{task.base_commit.slice(0, 7)} → {task.head_commit.slice(0, 7)}</code>
                <span className="meta-label" style={{ marginLeft: 'auto' }}>Trace ID:</span>
                <CorrelationLink correlationId={task.correlation_id} />
              </div>

              {/* Task Description */}
              <div className="task-detail-section">
                <h4 className="section-subtitle">Task Objective</h4>
                <p className="task-desc-text">{task.description}</p>
              </div>

              {/* Lifecycle History Stepper */}
              <div className="task-detail-section">
                <h4 className="section-subtitle">Lifecycle History</h4>
                <div className="lifecycle-timeline">
                  {task.lifecycle_history?.map((event, idx) => (
                    <div key={idx} className="timeline-item">
                      <div className="timeline-dot" />
                      <div className="timeline-content">
                        <div className="timeline-header">
                          <span className="timeline-state font-mono">[{event.state.toUpperCase()}]</span>
                          <span className="timeline-actor">by {event.actor}</span>
                          <span className="timeline-time text-dim text-xs">
                            {new Date(event.timestamp).toLocaleTimeString()}
                          </span>
                        </div>
                        <p className="timeline-msg">{event.message}</p>
                      </div>
                    </div>
                  ))}
                </div>
              </div>

              {/* Executed Runs */}
              {task.runs && task.runs.length > 0 && (
                <div className="task-detail-section">
                  <div className="section-header">
                    <h4 className="section-subtitle">Associated Execution Runs</h4>
                    <Button variant="ghost" size="sm" onClick={() => onNavigateRuns?.(task.id)}>
                      View All Runs →
                    </Button>
                  </div>
                  <div className="task-runs-list">
                    {task.runs.map((r) => (
                      <div key={r.run_id} className="task-run-row">
                        <code className="run-id-code">{r.run_id}</code>
                        <StatusBadge status="ready" label={r.status.toUpperCase()} />
                        <span className="run-steps">{r.step_count} steps</span>
                        <span className="run-duration text-dim text-xs">{r.duration_ms}ms</span>
                      </div>
                    ))}
                  </div>
                </div>
              )}
            </div>
          )}
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
