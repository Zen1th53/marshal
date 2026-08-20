import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Trace } from './Trace';
import { api } from '../api/client';

const mockTraceData = {
  target_id: 'TASK-002-CONTROL-PLANE',
  root_node: {
    id: 'TASK-002-CONTROL-PLANE',
    type: 'task',
    title: 'Mission Control Web Plane',
    producer: 'operator-zen1th53',
    timestamp: '2026-08-20T00:00:00Z',
    relationship: 'root',
    is_proven_binding: true,
  },
  nodes: [
    {
      id: 'TASK-002-CONTROL-PLANE',
      type: 'task',
      title: 'Mission Control Web Plane',
      producer: 'operator-zen1th53',
      timestamp: '2026-08-20T00:00:00Z',
      relationship: 'root',
      is_proven_binding: true,
    },
    {
      id: 'MEM-REV-491',
      type: 'memory_injection',
      title: 'Arch Invariants & Loopback Policy',
      producer: 'memory-subsystem',
      timestamp: '2026-08-20T00:00:00Z',
      relationship: 'injected_memory',
      is_proven_binding: true,
      parent_id: 'TASK-002-CONTROL-PLANE',
    },
    {
      id: 'req-trace-audit-099',
      type: 'audit_event',
      title: 'Correlation Log Attestation Trace',
      producer: 'system-tracing',
      timestamp: '2026-08-20T00:00:00Z',
      relationship: 'audited',
      is_proven_binding: false,
      parent_id: 'TASK-002-CONTROL-PLANE',
    },
  ],
  max_depth: 3,
  total_nodes: 3,
  generated_at: '2026-08-20T00:00:00Z',
};

describe('Trace Route (T195)', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('renders provenance nodes with proven binding and correlation badges', async () => {
    vi.spyOn(api, 'getProvenanceTrace').mockResolvedValueOnce(mockTraceData);

    render(<Trace />);
    expect(await screen.findByText('Causal Provenance & "Why" Trace')).toBeInTheDocument();
    expect(screen.getByText('Mission Control Web Plane')).toBeInTheDocument();
    expect(screen.getByText('Arch Invariants & Loopback Policy')).toBeInTheDocument();
    expect(screen.getAllByText(/PROVEN BINDING/i).length).toBeGreaterThanOrEqual(1);
    expect(screen.getByText(/CORRELATED/i)).toBeInTheDocument();
  });

  it('searches for new target trace on form submit', async () => {
    const traceSpy = vi.spyOn(api, 'getProvenanceTrace').mockResolvedValue(mockTraceData);

    render(<Trace />);
    expect(await screen.findByText('Causal Provenance & "Why" Trace')).toBeInTheDocument();

    const input = screen.getByLabelText(/Target ID/i);
    await userEvent.clear(input);
    await userEvent.type(input, 'TASK-003-SECURITY-AUDIT');

    const submitBtn = screen.getByRole('button', { name: /Reconstruct Trace/i });
    await userEvent.click(submitBtn);

    expect(traceSpy).toHaveBeenCalledWith('TASK-003-SECURITY-AUDIT');
  });
});
