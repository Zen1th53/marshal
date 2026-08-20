import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryInfluenceTrace } from './MemoryInfluenceTrace';
import { api } from '../../../api/client';

const mockTraceData = {
  memory_id: 'MEM-001-ARCH-DECISION',
  title: 'Loopback Architecture Invariant',
  total_recalls: 3,
  total_injections: 2,
  total_citations: 1,
  events: [
    {
      event_id: 'EV-RECALL-001',
      event_type: 'cited_in_action',
      task_id: 'TASK-001',
      run_id: 'RUN-TASK-001-01',
      agent_id: 'agent-arch-lead',
      revision_used: 1,
      evidence_plan_id: 'EVID-001-TESTS',
      causal_link_status: 'direct_citation',
      timestamp: '2026-08-20T00:00:00Z',
    },
  ],
};

describe('MemoryInfluenceTrace Component (T207)', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('renders read receipt counts and event timeline', async () => {
    vi.spyOn(api, 'getMemoryUsageTrace').mockResolvedValueOnce(mockTraceData);

    render(<MemoryInfluenceTrace memoryId="MEM-001-ARCH-DECISION" onClose={vi.fn()} />);

    expect(await screen.findByText('Memory Read Receipts & Causal Influence Trace')).toBeInTheDocument();
    expect(screen.getByText('EV-RECALL-001')).toBeInTheDocument();
    expect(screen.getByText('CITED IN ACTION')).toBeInTheDocument();
    expect(screen.getByText('agent-arch-lead')).toBeInTheDocument();
  });

  it('calls onClose when close button is clicked', async () => {
    vi.spyOn(api, 'getMemoryUsageTrace').mockResolvedValue(mockTraceData);
    const closeFn = vi.fn();

    render(<MemoryInfluenceTrace memoryId="MEM-001-ARCH-DECISION" onClose={closeFn} />);
    const closeBtn = await screen.findByLabelText('Close');
    await userEvent.click(closeBtn);

    expect(closeFn).toHaveBeenCalled();
  });
});
