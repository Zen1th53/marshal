import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Review } from './Review';
import { api } from '../api/client';

const mockReviewData = {
  items: [
    {
      task_id: 'TASK-002-CONTROL-PLANE',
      title: 'Mission Control Web Plane',
      stage: 'gate_review',
      risk: 'CRITICAL',
      owner: 'agent-codex-implementer',
      base_commit: 'e174534',
      head_commit: '29c3643',
      is_stale_head: false,
      approvals_count: 1,
      required_quorum: 2,
      blocker_count: 0,
      submitted_at: '2026-08-20T00:00:00Z',
    },
    {
      task_id: 'TASK-004-BENCHMARKS',
      title: 'Latency Benchmarks',
      stage: 'plan_review',
      risk: 'MEDIUM',
      owner: 'agent-opencode-local',
      base_commit: '1b29175',
      head_commit: '1b29175',
      is_stale_head: true,
      approvals_count: 0,
      required_quorum: 1,
      blocker_count: 1,
      submitted_at: '2026-08-20T00:00:00Z',
    },
  ],
  total_count: 2,
  limit: 50,
  offset: 0,
};

describe('Review Center (T191)', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('renders review queue items, stage badges, and stale warning pills', async () => {
    vi.spyOn(api, 'getReviewQueue').mockResolvedValueOnce(mockReviewData);

    render(<Review />);
    expect(await screen.findByText('Evidence & Quorum Review Center')).toBeInTheDocument();
    expect(screen.getByText('Mission Control Web Plane')).toBeInTheDocument();
    expect(screen.getByText('GATE REVIEW')).toBeInTheDocument();
    expect(screen.getByText('PLAN REVIEW')).toBeInTheDocument();
    expect(screen.getByText('STALE')).toBeInTheDocument();
    expect(screen.getByText('1 / 2 Signed')).toBeInTheDocument();
  });

  it('filters queue by stage select option', async () => {
    const queueSpy = vi.spyOn(api, 'getReviewQueue').mockResolvedValue(mockReviewData);

    render(<Review />);
    expect(await screen.findByText('Evidence & Quorum Review Center')).toBeInTheDocument();

    const stageSelect = screen.getByLabelText(/Filter by review stage/i);
    await userEvent.selectOptions(stageSelect, 'gate_review');

    expect(queueSpy).toHaveBeenCalledWith(
      expect.objectContaining({
        stage: 'gate_review',
      })
    );
  });
});
