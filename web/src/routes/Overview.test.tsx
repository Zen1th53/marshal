import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Overview } from './Overview';
import { api } from '../api/client';

const mockOverviewData = {
  system_status: {
    state: 'READY' as const,
    version: '1.0.0',
    commit_sha: '67816af',
    database_schema: 'v67',
    active_workers: 0,
    pending_tasks: 0,
    updated_at: '2026-08-20T00:00:00Z',
  },
  tasks: {
    active: 3,
    queued: 5,
    blocked: 1,
    review: 2,
    completed: 42,
    failed: 0,
    total: 53,
  },
  agents: {
    total: 4,
    active: 2,
    idle: 2,
  },
  providers: [
    {
      name: 'codex',
      binary_name: 'codex',
      installed: true,
      state: 'READY' as const,
      version: '1.0.0',
      probed_at: '2026-08-20T00:00:00Z',
    },
  ],
  memory_health: 'OPTIMAL',
  security_notices: [
    {
      level: 'INFO',
      title: 'Strict CSP Active',
      message: 'Isolation enforced',
      created_at: '2026-08-20T00:00:00Z',
    },
  ],
  evaluated_at: '2026-08-20T00:00:00Z',
};

describe('Overview Route (T181)', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('renders factual telemetry and metric cards', async () => {
    vi.spyOn(api, 'getOverview').mockResolvedValueOnce(mockOverviewData);

    render(<Overview />);
    expect(await screen.findByText('Mission Control Overview')).toBeInTheDocument();
    expect(screen.getByText('Active Tasks')).toBeInTheDocument();
    expect(screen.getByText('3')).toBeInTheDocument();
    expect(screen.getByText('Completed Tasks')).toBeInTheDocument();
    expect(screen.getByText('42')).toBeInTheDocument();
    expect(screen.getByText('CODEX')).toBeInTheDocument();
    expect(screen.getByText('Strict CSP Active')).toBeInTheDocument();
  });

  it('navigates to filtered views on card click', async () => {
    vi.spyOn(api, 'getOverview').mockResolvedValueOnce(mockOverviewData);
    const onNavigate = vi.fn();

    render(<Overview onNavigate={onNavigate} />);
    const reviewCard = (await screen.findByText('Awaiting Review')).closest('.metric-card');
    expect(reviewCard).not.toBeNull();
    if (reviewCard) {
      await userEvent.click(reviewCard);
      expect(onNavigate).toHaveBeenCalledWith('review');
    }
  });
});
