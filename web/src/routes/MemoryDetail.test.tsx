import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryDetail } from './MemoryDetail';
import { api } from '../api/client';

const mockDetailData = {
  id: 'MEM-001-ARCH-DECISION',
  project_id: 'PROJ-MARSHAL-MAIN',
  scope: 'project',
  scope_id: 'PROJ-MARSHAL-MAIN',
  kind: 'decision',
  title: 'Loopback Architecture Invariant',
  body: 'Web Control Plane MUST strictly bind to 127.0.0.1 loopback interface.',
  lifecycle: 'active',
  authority: 'verified',
  confidence: 0.99,
  digest_sha256: '9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08',
  revision: 3,
  is_encrypted: false,
  observed_at: '2026-08-20T00:00:00Z',
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
};

describe('MemoryDetail Component (T201)', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('renders memory record details, provenance, and digest', async () => {
    vi.spyOn(api, 'getMemoryDetail').mockResolvedValueOnce(mockDetailData);

    render(<MemoryDetail memoryId="MEM-001-ARCH-DECISION" onClose={vi.fn()} />);
    expect(await screen.findByText('Loopback Architecture Invariant')).toBeInTheDocument();
    expect(screen.getByText('MEM-001-ARCH-DECISION')).toBeInTheDocument();
    expect(screen.getByText('agent-arch-lead')).toBeInTheDocument();
    expect(screen.getByText('EVID-001-TESTS')).toBeInTheDocument();
    expect(screen.getByText(/9f86d08188/i)).toBeInTheDocument();
  });

  it('calls onClose when close button clicked', async () => {
    vi.spyOn(api, 'getMemoryDetail').mockResolvedValueOnce(mockDetailData);
    const closeFn = vi.fn();

    render(<MemoryDetail memoryId="MEM-001-ARCH-DECISION" onClose={closeFn} />);
    const closeBtn = await screen.findByLabelText('Close');
    await userEvent.click(closeBtn);

    expect(closeFn).toHaveBeenCalled();
  });
});
