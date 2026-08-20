import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import { TaskDetail } from './TaskDetail';
import { api } from '../api/client';

const mockTaskDetailData = {
  id: 'TASK-001-CORE-MEMORY',
  title: 'Implement Tiered Memory',
  description: 'Build bidirectional SQLite indices and vector engine.',
  status: 'completed' as const,
  risk: 'HIGH',
  assigned_to: 'agent-claude-planner',
  base_commit: '1a2b3c4',
  head_commit: '5e6f7g8',
  head_mismatch_detected: false,
  approvals_count: 2,
  required_quorum: 2,
  stale_approval_detected: false,
  correlation_id: 'req-audit-TASK-001',
  created_at: '2026-08-20T00:00:00Z',
  updated_at: '2026-08-20T00:00:00Z',
  lifecycle_history: [
    {
      timestamp: '2026-08-20T00:00:00Z',
      actor: 'operator',
      state: 'created',
      message: 'Task created from mission plan',
    },
    {
      timestamp: '2026-08-20T00:05:00Z',
      actor: 'agent-claude-planner',
      state: 'completed',
      message: 'Task completed successfully',
    },
  ],
  runs: [
    {
      run_id: 'RUN-001-01',
      status: 'success',
      step_count: 12,
      duration_ms: 4250,
      started_at: '2026-08-20T00:01:00Z',
    },
  ],
};

describe('TaskDetail (T185)', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('renders task metadata, correlation link, and lifecycle history', async () => {
    vi.spyOn(api, 'getTaskDetail').mockResolvedValueOnce(mockTaskDetailData);
    const onClose = vi.fn();

    render(<TaskDetail taskId="TASK-001-CORE-MEMORY" onClose={onClose} />);
    expect(await screen.findByText('Implement Tiered Memory')).toBeInTheDocument();
    expect(screen.getByText('Quorum Approvals')).toBeInTheDocument();
    expect(screen.getByText('2 / 2')).toBeInTheDocument();
    expect(screen.getByText('Task Objective')).toBeInTheDocument();
    expect(screen.getByText(/Build bidirectional SQLite indices/i)).toBeInTheDocument();
    expect(screen.getByText('[CREATED]')).toBeInTheDocument();
    expect(screen.getByText('[COMPLETED]')).toBeInTheDocument();
    expect(screen.getByText('RUN-001-01')).toBeInTheDocument();
  });

  it('flags stale approvals warning banner when detected', async () => {
    vi.spyOn(api, 'getTaskDetail').mockResolvedValueOnce({
      ...mockTaskDetailData,
      stale_approval_detected: true,
    });

    render(<TaskDetail taskId="TASK-001-CORE-MEMORY" onClose={vi.fn()} />);
    expect(await screen.findByText(/Stale Approvals Detected/i)).toBeInTheDocument();
  });
});
