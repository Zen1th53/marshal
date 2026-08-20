import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { TaskActionControls } from './TaskActionControls';
import { ToastProvider } from '../../../components/toast';
import { AuthContext, type AuthContextValue } from '../../../auth/AuthContext';
import { api } from '../../../api/client';

function renderControls(status: 'pending' | 'ready' | 'running' | 'failed', onComplete = vi.fn()) {
  const auth: AuthContextValue = {
    user: { principal_id: 'operator-1', role: 'admin', authorities: ['task:cancel', 'task:run'] },
    isAuthenticated: true,
    isLoading: false,
    login: vi.fn(),
    logout: vi.fn(),
    refreshSession: vi.fn(),
  };

  return render(
    <AuthContext.Provider value={auth}>
      <ToastProvider>
        <TaskActionControls taskId="TASK-001" status={status} onActionComplete={onComplete} />
      </ToastProvider>
    </AuthContext.Provider>
  );
}

describe('TaskActionControls (T187)', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('triggers runTask on Start Run click for ready task', async () => {
    const runSpy = vi.spyOn(api, 'runTask').mockResolvedValueOnce({
      run_id: 'RUN-001-99',
      task_id: 'TASK-001',
      status: 'running',
      started_at: '2026-08-20T00:00:00Z',
    });
    const onComplete = vi.fn();

    renderControls('ready', onComplete);
    const startBtn = screen.getByRole('button', { name: /Start Run/i });
    await userEvent.click(startBtn);

    expect(runSpy).toHaveBeenCalledWith('TASK-001');
    expect(onComplete).toHaveBeenCalled();
  });

  it('renders confirmation modal when canceling running task', async () => {
    const cancelSpy = vi.spyOn(api, 'cancelTask').mockResolvedValueOnce({
      task_id: 'TASK-001',
      status: 'canceled',
      canceled_at: '2026-08-20T00:00:00Z',
    });
    const onComplete = vi.fn();

    renderControls('running', onComplete);
    const cancelBtn = screen.getByRole('button', { name: /Cancel Task/i });
    await userEvent.click(cancelBtn);

    // Confirmation dialog appears
    expect(screen.getByText('Confirm Task Cancellation')).toBeInTheDocument();
    const confirmBtn = screen.getByRole('button', { name: /Confirm/i });
    await userEvent.click(confirmBtn);

    expect(cancelSpy).toHaveBeenCalledWith('TASK-001');
    expect(onComplete).toHaveBeenCalled();
  });
});
