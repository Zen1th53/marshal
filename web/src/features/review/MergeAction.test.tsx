import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MergeAction } from './MergeAction';
import { ToastProvider } from '../../components/toast';
import { AuthContext, type AuthContextValue } from '../../auth/AuthContext';
import { api } from '../../api/client';

const mockPreflightEligible = {
  task_id: 'TASK-003',
  is_eligible: true,
  expected_head: '7d17fb8',
  target_branch: 'main',
  quorum_met: true,
  has_veto: false,
  is_stale_head: false,
  gating_checks: ['lint: PASS', 'tests: PASS (59/59)'],
};

function renderWithAuth(ui: React.ReactElement) {
  const auth: AuthContextValue = {
    user: { principal_id: 'operator-1', role: 'admin', authorities: ['task:merge'] },
    isAuthenticated: true,
    isLoading: false,
    login: vi.fn(),
    logout: vi.fn(),
    refreshSession: vi.fn(),
  };

  return render(
    <AuthContext.Provider value={auth}>
      <ToastProvider>{ui}</ToastProvider>
    </AuthContext.Provider>
  );
}

describe('MergeAction (T193)', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('renders preflight gate checklist and allows merge for eligible task', async () => {
    vi.spyOn(api, 'getMergePreflight').mockResolvedValueOnce(mockPreflightEligible);
    const executeSpy = vi.spyOn(api, 'executeMerge').mockResolvedValueOnce({
      task_id: 'TASK-003',
      merged: true,
      merge_commit: 'mrg-9999-7d17fb8',
      target_branch: 'main',
      merged_at: '2026-08-20T00:00:00Z',
      correlation_id: 'req-merge-TASK-003',
    });

    renderWithAuth(<MergeAction taskId="TASK-003" />);
    expect(await screen.findByText('ELIGIBLE TO MERGE')).toBeInTheDocument();
    expect(screen.getByText('tests: PASS (59/59)')).toBeInTheDocument();

    const mergeBtn = screen.getByRole('button', { name: /Finalize & Merge Task/i });
    await userEvent.click(mergeBtn);

    // Confirm dialog
    expect(screen.getByText('Confirm Codebase Merge')).toBeInTheDocument();
    const confirmBtn = screen.getByRole('button', { name: /Confirm/i });
    await userEvent.click(confirmBtn);

    expect(executeSpy).toHaveBeenCalledWith('TASK-003', {
      expected_head: '7d17fb8',
      strategy: 'squash',
    });
    expect(await screen.findByText('Task Codebase Merged Successfully')).toBeInTheDocument();
  });
});
