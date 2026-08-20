import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Tasks } from './Tasks';
import { api } from '../api/client';

const mockTasksResponse = {
  items: [
    {
      id: 'TASK-001',
      title: 'Core Memory Indices',
      status: 'completed' as const,
      risk: 'HIGH',
      assigned_to: 'agent-claude-planner',
      base_commit: '1a2b3c4',
      head_commit: '5e6f7g8',
      created_at: '2026-08-20T00:00:00Z',
      updated_at: '2026-08-20T00:00:00Z',
    },
    {
      id: 'TASK-002',
      title: 'Realtime Control Plane',
      status: 'running' as const,
      risk: 'CRITICAL',
      assigned_to: 'agent-codex-implementer',
      base_commit: 'bc1e991',
      head_commit: '4431cce',
      created_at: '2026-08-20T00:00:00Z',
      updated_at: '2026-08-20T00:00:00Z',
    },
  ],
  total: 2,
  limit: 50,
};

describe('Tasks Route (T183)', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('renders tasks list and navigates saved views', async () => {
    vi.spyOn(api, 'getTasks').mockResolvedValueOnce(mockTasksResponse);

    render(<Tasks />);
    expect(await screen.findByText('Task Explorer & Mission Queue')).toBeInTheDocument();
    expect(screen.getByText('Core Memory Indices')).toBeInTheDocument();
    expect(screen.getByText('Realtime Control Plane')).toBeInTheDocument();

    // Click saved view preset for Running
    const runningPreset = screen.getByRole('button', { name: /In-Flight/i });
    await userEvent.click(runningPreset);

    expect(screen.getByText('Realtime Control Plane')).toBeInTheDocument();
    expect(screen.queryByText('Core Memory Indices')).toBeNull();
  });

  it('filters by search keyword', async () => {
    vi.spyOn(api, 'getTasks').mockResolvedValueOnce(mockTasksResponse);

    render(<Tasks />);
    const searchInput = await screen.findByPlaceholderText(/Search tasks by ID or title/i);
    await userEvent.type(searchInput, 'control');

    expect(screen.getByText('Realtime Control Plane')).toBeInTheDocument();
    expect(screen.queryByText('Core Memory Indices')).toBeNull();
  });
});
