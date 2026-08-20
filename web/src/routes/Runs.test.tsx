import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Runs } from './Runs';
import { api } from '../api/client';

const mockRunsData = {
  items: [
    {
      run_id: 'RUN-TASK-001-01',
      task_id: 'TASK-001-CORE-MEMORY',
      agent_id: 'agent-claude-planner',
      provider: 'anthropic',
      status: 'succeeded',
      duration_ms: 4250,
      step_count: 12,
      evidence_count: 3,
      base_commit: '4431cce',
      head_commit: 'e174534',
      started_at: '2026-08-20T00:00:00Z',
    },
    {
      run_id: 'RUN-TASK-002-01',
      task_id: 'TASK-002-CONTROL-PLANE',
      agent_id: 'agent-codex-implementer',
      provider: 'openai',
      status: 'running',
      duration_ms: 18200,
      step_count: 24,
      evidence_count: 2,
      base_commit: 'e174534',
      head_commit: '3db9f8b',
      started_at: '2026-08-20T01:00:00Z',
    },
  ],
  total_count: 2,
  limit: 50,
  offset: 0,
};

describe('Runs (T188)', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('renders execution runs table with durations and step counts', async () => {
    vi.spyOn(api, 'listRuns').mockResolvedValueOnce(mockRunsData);

    render(<Runs />);
    expect(await screen.findByText('Execution Run Explorer')).toBeInTheDocument();
    expect(screen.getByText('RUN-TASK-001-01')).toBeInTheDocument();
    expect(screen.getByText('RUN-TASK-002-01')).toBeInTheDocument();
    expect(screen.getByText('4250ms')).toBeInTheDocument();
    expect(screen.getByText('12')).toBeInTheDocument();
    expect(screen.getByText('3 items')).toBeInTheDocument();
  });

  it('filters runs by status selector', async () => {
    const listRunsSpy = vi.spyOn(api, 'listRuns').mockResolvedValue(mockRunsData);

    render(<Runs />);
    expect(await screen.findByText('Execution Run Explorer')).toBeInTheDocument();

    const statusSelect = screen.getByLabelText(/Filter runs by status/i);
    await userEvent.selectOptions(statusSelect, 'running');

    expect(listRunsSpy).toHaveBeenCalledWith(
      expect.objectContaining({
        status: 'running',
      })
    );
  });
});
