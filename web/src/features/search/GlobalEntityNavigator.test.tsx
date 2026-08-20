import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { GlobalEntityNavigator } from './GlobalEntityNavigator';
import { api } from '../../api/client';

const mockSearchData = {
  query: 'TSK-001',
  total_matches: 1,
  results: [
    {
      entity_type: 'task',
      id: 'TSK-001',
      title: 'Analyze codebase AST graph',
      subtitle: 'Task · Priority: P0',
      route_target: '/tasks/TSK-001',
      badge_status: 'COMPLETED',
      score: 1.0,
    },
  ],
  evaluated_at: '2026-08-20T00:00:00Z',
};

describe('GlobalEntityNavigator Component (T215)', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('renders search input and shows matching entities', async () => {
    vi.spyOn(api, 'searchGlobal').mockResolvedValue(mockSearchData);

    render(<GlobalEntityNavigator isOpen={true} onClose={vi.fn()} onNavigate={vi.fn()} />);

    const input = screen.getByPlaceholderText(/Search tasks, runs, agents/i);
    expect(input).toBeInTheDocument();

    await userEvent.type(input, 'TSK-001');

    expect(await screen.findByText('Analyze codebase AST graph')).toBeInTheDocument();
    expect(screen.getByText('TSK-001')).toBeInTheDocument();
    expect(screen.getByText('TASK')).toBeInTheDocument();
  });

  it('navigates to selected entity on click', async () => {
    vi.spyOn(api, 'searchGlobal').mockResolvedValue(mockSearchData);
    const navFn = vi.fn();
    const closeFn = vi.fn();

    render(<GlobalEntityNavigator isOpen={true} onClose={closeFn} onNavigate={navFn} />);

    const input = screen.getByPlaceholderText(/Search tasks, runs, agents/i);
    await userEvent.type(input, 'TSK-001');

    const resultOption = await screen.findByText('Analyze codebase AST graph');
    await userEvent.click(resultOption);

    expect(navFn).toHaveBeenCalledWith('tasks');
    expect(closeFn).toHaveBeenCalled();
  });
});
