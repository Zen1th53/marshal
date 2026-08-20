import { useState, useEffect, useCallback } from 'react';
import { api } from '../api/client';
import { useRealtimeEvent } from '../realtime/useRealtime';
import { StatusBadge, Button } from '../components/ui';
import { LoadingState, ErrorState, EmptyState } from '../components/state';
import { TaskDetail } from './TaskDetail';
import { CreateTaskModal } from '../features/tasks/forms/CreateTaskModal';
import type { TaskSummaryDTO, TaskStatus } from '../api/types';

interface TasksProps {
  onNavigateRuns?: (taskId: string) => void;
}

export function Tasks({ onNavigateRuns }: TasksProps) {
  const [tasks, setTasks] = useState<TaskSummaryDTO[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [search, setSearch] = useState('');
  const [statusFilter, setStatusFilter] = useState<string>('all');
  const [riskFilter, setRiskFilter] = useState<string>('all');
  const [selectedTaskId, setSelectedTaskId] = useState<string | null>(null);
  const [isCreateOpen, setIsCreateOpen] = useState(false);

  const fetchTasks = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const resp = await api.getTasks();
      setTasks(resp.items ?? []);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to fetch tasks');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void fetchTasks();
  }, [fetchTasks]);

  useRealtimeEvent('task.status', () => {
    void fetchTasks();
  });

  const filteredTasks = tasks.filter((t) => {
    const matchesSearch =
      t.id.toLowerCase().includes(search.toLowerCase()) ||
      t.title.toLowerCase().includes(search.toLowerCase());
    const matchesStatus =
      statusFilter === 'all' ||
      t.status.toLowerCase() === statusFilter.toLowerCase();
    const matchesRisk =
      riskFilter === 'all' ||
      t.risk.toUpperCase() === riskFilter.toUpperCase();
    return matchesSearch && matchesStatus && matchesRisk;
  });

  const getStatusBadgeType = (status: TaskStatus) => {
    switch (status) {
      case 'completed':
        return 'ready';
      case 'running':
        return 'ready';
      case 'ready':
      case 'pending':
        return 'degraded';
      case 'failed':
      case 'canceled':
        return 'unavailable';
      default:
        return 'not_run';
    }
  };

  return (
    <div className="tasks-container">
      <div className="tasks-header">
        <div className="tasks-headline">
          <h2 className="tasks-title">Task Explorer & Mission Queue</h2>
          <span className="tasks-count">{tasks.length} Total Registered Tasks</span>
        </div>
        <div className="tasks-header-actions" style={{ display: 'flex', gap: 'var(--space-2)' }}>
          <Button variant="primary" size="sm" onClick={() => setIsCreateOpen(true)}>
            + New Task
          </Button>
          <Button variant="secondary" size="sm" onClick={fetchTasks}>
            Refresh Queue
          </Button>
        </div>
      </div>

      {/* Filter Controls & Saved Views */}
      <div className="task-filter-panel">
        <div className="search-input-wrapper">
          <span className="search-icon" aria-hidden="true">🔍</span>
          <input
            type="text"
            className="filter-input"
            placeholder="Search tasks by ID or title…"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
        </div>

        <div className="filter-group">
          <span className="filter-label">Status:</span>
          <select
            className="filter-select"
            value={statusFilter}
            onChange={(e) => setStatusFilter(e.target.value)}
            aria-label="Filter by task status"
          >
            <option value="all">All Statuses</option>
            <option value="running">Running</option>
            <option value="ready">Ready</option>
            <option value="pending">Pending</option>
            <option value="completed">Completed</option>
            <option value="failed">Failed</option>
          </select>
        </div>

        <div className="filter-group">
          <span className="filter-label">Risk:</span>
          <select
            className="filter-select"
            value={riskFilter}
            onChange={(e) => setRiskFilter(e.target.value)}
            aria-label="Filter by task risk"
          >
            <option value="all">All Risks</option>
            <option value="LOW">Low</option>
            <option value="MEDIUM">Medium</option>
            <option value="HIGH">High</option>
            <option value="CRITICAL">Critical</option>
          </select>
        </div>
      </div>

      {/* Saved View Presets */}
      <div className="saved-presets-bar">
        <span className="presets-label">Saved Views:</span>
        <button
          type="button"
          className={`preset-btn ${statusFilter === 'all' && riskFilter === 'all' ? 'active' : ''}`}
          onClick={() => {
            setStatusFilter('all');
            setRiskFilter('all');
            setSearch('');
          }}
        >
          All Tasks
        </button>
        <button
          type="button"
          className={`preset-btn ${statusFilter === 'running' ? 'active' : ''}`}
          onClick={() => {
            setStatusFilter('running');
            setRiskFilter('all');
          }}
        >
          ⚡ In-Flight (Running)
        </button>
        <button
          type="button"
          className={`preset-btn ${riskFilter === 'CRITICAL' ? 'active' : ''}`}
          onClick={() => {
            setRiskFilter('CRITICAL');
            setStatusFilter('all');
          }}
        >
          🚨 Critical Risk
        </button>
        <button
          type="button"
          className={`preset-btn ${statusFilter === 'completed' ? 'active' : ''}`}
          onClick={() => {
            setStatusFilter('completed');
            setRiskFilter('all');
          }}
        >
          ✓ Completed
        </button>
      </div>

      {/* Tasks Table */}
      {loading && tasks.length === 0 ? (
        <LoadingState message="Scanning task board state…" />
      ) : error ? (
        <ErrorState severity="error" message={error} onRetry={fetchTasks} />
      ) : filteredTasks.length === 0 ? (
        <EmptyState
          title="No Tasks Found"
          description={search ? 'No tasks match the active filter query.' : 'No tasks recorded in workspace.'}
        />
      ) : (
        <div className="tasks-table-wrapper">
          <table className="tasks-table" aria-label="Task List">
            <thead>
              <tr>
                <th>Task ID</th>
                <th>Title</th>
                <th>Status</th>
                <th>Risk</th>
                <th>Assigned Agent</th>
                <th>Base → Head</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {filteredTasks.map((t) => (
                <tr key={t.id} onClick={() => setSelectedTaskId(t.id)} style={{ cursor: 'pointer' }}>
                  <td>
                    <code className="task-id-code">{t.id}</code>
                  </td>
                  <td className="task-title-cell">{t.title}</td>
                  <td>
                    <StatusBadge
                      status={getStatusBadgeType(t.status)}
                      label={t.status.toUpperCase()}
                    />
                  </td>
                  <td>
                    <span className={`risk-badge risk-${t.risk.toLowerCase()}`}>{t.risk}</span>
                  </td>
                  <td>
                    {t.assigned_to ? (
                      <code className="agent-ref">{t.assigned_to}</code>
                    ) : (
                      <span className="text-dim">Unassigned</span>
                    )}
                  </td>
                  <td>
                    <span className="commit-range font-mono text-xs">
                      {t.base_commit.slice(0, 7)} → {t.head_commit.slice(0, 7)}
                    </span>
                  </td>
                  <td>
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={(e) => {
                        e.stopPropagation();
                        onNavigateRuns?.(t.id);
                      }}
                      aria-label={`View execution runs for ${t.id}`}
                    >
                      Runs →
                    </Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {selectedTaskId && (
        <TaskDetail
          taskId={selectedTaskId}
          onClose={() => setSelectedTaskId(null)}
          onNavigateRuns={onNavigateRuns}
        />
      )}

      <CreateTaskModal
        isOpen={isCreateOpen}
        onClose={() => setIsCreateOpen(false)}
        onSuccess={fetchTasks}
      />
    </div>
  );
}
