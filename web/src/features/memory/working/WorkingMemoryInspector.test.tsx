import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { WorkingMemoryInspector } from './WorkingMemoryInspector';
import { ToastProvider } from '../../../components/toast';
import { api } from '../../../api/client';

const mockWorkingData = {
  slots: [
    {
      slot_key: 'scratch:plan-notes',
      owner_scope: 'session',
      scope_id: 'SES-DEV-01',
      content: 'Verify cryptographic signature parity on Ed25519',
      revision: 2,
      is_pinned: false,
      is_private: false,
      allocated_bytes: 50,
      expires_at: '2026-08-20T04:00:00Z',
      last_updated_at: '2026-08-20T00:00:00Z',
    },
  ],
  total_quota_bytes: 65536,
  used_bytes: 50,
  eviction_strategy: 'LRU',
};

describe('WorkingMemoryInspector Component (T204)', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('renders working memory slots and quota meters', async () => {
    vi.spyOn(api, 'getWorkingMemory').mockResolvedValueOnce(mockWorkingData);

    render(
      <ToastProvider>
        <WorkingMemoryInspector />
      </ToastProvider>
    );

    expect(await screen.findByText('scratch:plan-notes')).toBeInTheDocument();
    expect(screen.getByText('Verify cryptographic signature parity on Ed25519')).toBeInTheDocument();
    expect(screen.getByText(/50 \/ 65536 Bytes/i)).toBeInTheDocument();
  });

  it('triggers candidate promotion on button click', async () => {
    vi.spyOn(api, 'getWorkingMemory').mockResolvedValue(mockWorkingData);
    const promoteSpy = vi.spyOn(api, 'promoteWorkingSlot').mockResolvedValueOnce({
      slot_key: 'scratch:plan-notes',
      candidate_memory_id: 'MEM-CAND-001',
      status: 'candidate_enqueued',
      message: 'Candidate enqueued for review',
    });

    render(
      <ToastProvider>
        <WorkingMemoryInspector />
      </ToastProvider>
    );

    const promoteBtn = await screen.findByRole('button', { name: /Promote to Candidate/i });
    await userEvent.click(promoteBtn);

    expect(promoteSpy).toHaveBeenCalledWith({
      slot_key: 'scratch:plan-notes',
      target_title: 'Promoted: scratch:plan-notes',
    });
  });
});
