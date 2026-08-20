import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Memory } from './Memory';
import { api } from '../api/client';

const mockMemoryData = {
  items: [
    {
      id: 'MEM-001-ARCH-DECISION',
      project_id: 'PROJ-MARSHAL-MAIN',
      scope: 'project',
      scope_id: 'PROJ-MARSHAL-MAIN',
      kind: 'decision',
      title: 'Loopback Architecture Invariant',
      body: 'Web Control Plane MUST strictly bind to 127.0.0.1.',
      lifecycle: 'active',
      authority: 'verified',
      confidence: 0.99,
      observed_at: '2026-08-20T00:00:00Z',
      retrieval_score: 0.95,
      retrieval_reason: 'Exact lexical match',
    },
  ],
  total_count: 1,
  limit: 50,
  offset: 0,
  index_status: 'healthy',
};

describe('Memory Route (T200)', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('renders memory search items and index status', async () => {
    vi.spyOn(api, 'searchMemory').mockResolvedValueOnce(mockMemoryData);

    render(<Memory />);
    expect(await screen.findByText('Memory Explorer & Hybrid Retrieval')).toBeInTheDocument();
    expect(screen.getByText('Loopback Architecture Invariant')).toBeInTheDocument();
    expect(screen.getByText('MEM-001-ARCH-DECISION')).toBeInTheDocument();
    expect(screen.getByText('HEALTHY')).toBeInTheDocument();
  });

  it('opens record detail modal on card click', async () => {
    vi.spyOn(api, 'searchMemory').mockResolvedValue(mockMemoryData);

    render(<Memory />);
    const card = await screen.findByText('Loopback Architecture Invariant');
    await userEvent.click(card);

    expect(screen.getByText('Content Body')).toBeInTheDocument();
    expect(screen.getByText('Retrieval Explanation')).toBeInTheDocument();
    expect(screen.getByText('Exact lexical match')).toBeInTheDocument();
  });
});
