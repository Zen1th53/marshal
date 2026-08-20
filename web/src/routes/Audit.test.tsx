import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Audit } from './Audit';
import { api } from '../api/client';

const mockAuditData = {
  events: [
    {
      id: 'AUD-001',
      actor: { principal_id: 'operator-zen1th53', role: 'admin' },
      action: 'task.merge',
      resource_type: 'task',
      resource_id: 'TASK-003-SECURITY-AUDIT',
      outcome: 'success',
      correlation_id: 'req-merge-TASK-003',
      timestamp: '2026-08-20T00:00:00Z',
      details: {},
    },
    {
      id: 'AUD-003',
      actor: { principal_id: 'unauthorized-guest', role: 'anonymous' },
      action: 'task.delete',
      resource_type: 'task',
      resource_id: 'TASK-001-CORE-MEMORY',
      outcome: 'denied',
      correlation_id: 'req-denied-003',
      timestamp: '2026-08-20T00:00:00Z',
      details: {},
    },
  ],
  total_count: 2,
  limit: 50,
  offset: 0,
};

describe('Audit Route (T199)', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('renders audit events, outcome badges, and correlation links', async () => {
    vi.spyOn(api, 'listAuditEvents').mockResolvedValueOnce(mockAuditData);

    render(<Audit />);
    expect(await screen.findByText('Global Governance & Audit Timeline')).toBeInTheDocument();
    expect(screen.getByText('AUD-001')).toBeInTheDocument();
    expect(screen.getByText('operator-zen1th53')).toBeInTheDocument();
    expect(screen.getByText('SUCCESS')).toBeInTheDocument();
    expect(screen.getByText('DENIED')).toBeInTheDocument();
  });

  it('filters audit logs by outcome selector', async () => {
    const listSpy = vi.spyOn(api, 'listAuditEvents').mockResolvedValue(mockAuditData);

    render(<Audit />);
    expect(await screen.findByText('Global Governance & Audit Timeline')).toBeInTheDocument();

    const outcomeSelect = screen.getByLabelText(/Filter by audit outcome/i);
    await userEvent.selectOptions(outcomeSelect, 'denied');

    expect(listSpy).toHaveBeenCalledWith(
      expect.objectContaining({
        outcome: 'denied',
      })
    );
  });
});
