import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryMutationActions } from './MemoryMutationActions';
import { ToastProvider } from '../../../components/toast';
import { api } from '../../../api/client';

describe('MemoryMutationActions Component (T205)', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('renders promote and tombstone buttons for candidate memory', async () => {
    render(
      <ToastProvider>
        <MemoryMutationActions
          memoryId="MEM-004-CANDIDATE"
          revision={1}
          digestSha256="abc123"
          lifecycle="candidate"
          onMutated={vi.fn()}
        />
      </ToastProvider>
    );

    expect(screen.getByRole('button', { name: /Promote to Durable/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Tombstone \/ Purge/i })).toBeInTheDocument();
  });

  it('promotes candidate memory to durable active state with rationale', async () => {
    const onMutated = vi.fn();
    const promoteSpy = vi.spyOn(api, 'promoteMemory').mockResolvedValueOnce({
      mutation_type: 'promote',
      memory_id: 'MEM-004-CANDIDATE',
      new_lifecycle: 'active',
      new_revision: 2,
      audit_id: 'AUD-001',
      signature_id: 'sig-001',
      mutated_at: '2026-08-20T00:00:00Z',
    });

    render(
      <ToastProvider>
        <MemoryMutationActions
          memoryId="MEM-004-CANDIDATE"
          revision={1}
          digestSha256="abc123"
          lifecycle="candidate"
          onMutated={onMutated}
        />
      </ToastProvider>
    );

    const openModalBtn = screen.getByRole('button', { name: /Promote to Durable/i });
    await userEvent.click(openModalBtn);

    expect(screen.getByText('Promote Memory Candidate to Durable Truth')).toBeInTheDocument();
    const textarea = screen.getByPlaceholderText(/Explain why this belief\/procedure invariant/i);
    await userEvent.type(textarea, 'Verified against multi-agent safety rules.');

    const confirmBtn = screen.getByRole('button', { name: /Confirm Promotion/i });
    await userEvent.click(confirmBtn);

    expect(promoteSpy).toHaveBeenCalledWith(
      expect.objectContaining({
        memory_id: 'MEM-004-CANDIDATE',
        review_rationale: 'Verified against multi-agent safety rules.',
      })
    );
    expect(onMutated).toHaveBeenCalled();
  });
});
