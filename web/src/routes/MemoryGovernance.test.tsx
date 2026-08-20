import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryGovernance } from './MemoryGovernance';
import { api } from '../api/client';

const mockGovernanceData = {
  items: [
    {
      id: 'GOV-CONF-001',
      category: 'conflicted',
      status: 'pending_review',
      target_memory_id: 'MEM-001-ARCH-DECISION',
      conflict_with_id: 'MEM-004-CANDIDATE-HEURISTIC',
      reason: 'Conflicting statements regarding strict loopback constraint',
      detected_at: '2026-08-20T00:00:00Z',
    },
  ],
  total_count: 1,
};

const mockConflictDiffData = {
  conflict_id: 'GOV-CONF-001',
  status: 'pending_review',
  resolution_mode: 'manual_review_required',
  base_memory: {
    id: 'MEM-001-ARCH-DECISION',
    title: 'Loopback Architecture Invariant',
    body: 'Strict 127.0.0.1 binding.',
    authority: 'verified',
    confidence: 0.99,
    scope: 'project',
    kind: 'decision',
    observed_at: '2026-08-20T00:00:00Z',
  },
  competing_memory: {
    id: 'MEM-004-CANDIDATE-HEURISTIC',
    title: 'Dynamic Token Pruning',
    body: 'Proxy candidate heuristic.',
    authority: 'provisional',
    confidence: 0.74,
    scope: 'session',
    kind: 'belief',
    observed_at: '2026-08-20T00:00:00Z',
  },
  detected_at: '2026-08-20T00:00:00Z',
};

describe('MemoryGovernance Route (T203)', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('renders governance queue items and category tabs', async () => {
    vi.spyOn(api, 'listGovernanceQueue').mockResolvedValueOnce(mockGovernanceData);

    render(<MemoryGovernance />);
    expect(await screen.findByText('Memory Governance, Stale & Conflict Workspace')).toBeInTheDocument();
    expect(screen.getByText('GOV-CONF-001')).toBeInTheDocument();
    expect(screen.getByText('CONFLICTED')).toBeInTheDocument();
    expect(screen.getByText('Conflicting statements regarding strict loopback constraint')).toBeInTheDocument();
  });

  it('opens side-by-side conflict diff modal on button click', async () => {
    vi.spyOn(api, 'listGovernanceQueue').mockResolvedValue(mockGovernanceData);
    vi.spyOn(api, 'getConflictComparison').mockResolvedValueOnce(mockConflictDiffData);

    render(<MemoryGovernance />);
    const compareBtn = await screen.findByRole('button', { name: /Compare Diff/i });
    await userEvent.click(compareBtn);

    expect(await screen.findByText('Side-by-Side Memory Conflict Diff')).toBeInTheDocument();
    expect(screen.getByText('BASE RECORD (Current)')).toBeInTheDocument();
    expect(screen.getByText('COMPETING RECORD')).toBeInTheDocument();
  });
});
