import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QuorumWorkspace } from './QuorumWorkspace';
import { ToastProvider } from '../../components/toast';
import { api } from '../../api/client';

const mockQuorumData = {
  task_id: 'TASK-002',
  head_commit: '29c3643',
  required_quorum: 2,
  current_approvals_count: 1,
  has_veto: false,
  is_quorum_met: false,
  independence_note: 'Quorum requires independent model providers.',
  attestations: [
    {
      reviewer_id: 'agent-claude-planner',
      provider: 'anthropic',
      role: 'planner_auditor',
      decision: 'approved',
      comment: 'Architecture conformance verified.',
      commit_hash: '29c3643',
      signed_at: '2026-08-20T00:00:00Z',
    },
  ],
};

describe('QuorumWorkspace (T192)', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('renders quorum progress count and recorded attestations', async () => {
    vi.spyOn(api, 'getTaskQuorum').mockResolvedValueOnce(mockQuorumData);

    render(
      <ToastProvider>
        <QuorumWorkspace taskId="TASK-002" />
      </ToastProvider>
    );

    expect(await screen.findByText('1 / 2 Independent Signatures')).toBeInTheDocument();
    expect(screen.getByText('agent-claude-planner')).toBeInTheDocument();
    expect(screen.getByText('Architecture conformance verified.')).toBeInTheDocument();
  });

  it('submits approval decision and updates state', async () => {
    vi.spyOn(api, 'getTaskQuorum').mockResolvedValue(mockQuorumData);
    const submitSpy = vi.spyOn(api, 'submitQuorumDecision').mockResolvedValueOnce({
      task_id: 'TASK-002',
      decision: 'approved',
      status: 'recorded',
    });

    render(
      <ToastProvider>
        <QuorumWorkspace taskId="TASK-002" />
      </ToastProvider>
    );

    const commentInput = await screen.findByLabelText(/Attestation Evidence/i);
    await userEvent.type(commentInput, 'Passed QA Gate');

    const submitBtn = screen.getByRole('button', { name: /Submit APPROVED/i });
    await userEvent.click(submitBtn);

    expect(submitSpy).toHaveBeenCalledWith('TASK-002', {
      decision: 'approved',
      comment: 'Passed QA Gate',
      commit_hash: '29c3643',
    });
  });
});
