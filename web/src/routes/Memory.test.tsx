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
    vi.spyOn(api, 'getMemoryDetail').mockResolvedValue({
      ...mockMemoryData.items[0],
      digest_sha256: '9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08',
      revision: 3,
      is_encrypted: false,
      provenance: {
        producer_agent_id: 'agent-arch-lead',
        source_run_id: 'RUN-TASK-001-01',
        correlation_id: 'req-audit-mem-001',
        evidence_ids: ['EVID-001-TESTS'],
        created_at: '2026-08-20T00:00:00Z',
      },
      lineage: {
        conflict_status: 'none',
        lineage_depth: 1,
      },
    });

    render(<Memory />);
    const card = await screen.findByText('Loopback Architecture Invariant');
    await userEvent.click(card);

    expect(await screen.findByText('Record Body Content')).toBeInTheDocument();
    expect(screen.getByText('Causal Provenance & Attestation')).toBeInTheDocument();
    expect(screen.getByText('agent-arch-lead')).toBeInTheDocument();
  });
});
