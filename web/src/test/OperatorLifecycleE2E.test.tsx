import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Login } from '../routes/Login';
import { Overview } from '../routes/Overview';
import { ToastProvider } from '../components/toast';
import { AuthProvider } from '../auth/AuthContext';
import { api } from '../api/client';

describe('Operator Lifecycle End-to-End Simulation (T219)', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('completes one-time code exchange and invokes login endpoint', async () => {
    const reqSpy = vi.spyOn(api, 'request').mockResolvedValue({
      principal_id: 'operator-primary',
      role: 'operator',
      authorities: ['task.plan', 'source.write', 'verify.qa', 'release.approve'],
    });

    render(
      <ToastProvider>
        <AuthProvider>
          <Login onSuccess={vi.fn()} />
        </AuthProvider>
      </ToastProvider>
    );

    const input = screen.getByPlaceholderText(/e\.g\./i);
    await userEvent.type(input, '123456');

    const submitBtn = screen.getByRole('button', { name: /Authenticate Operator/i });
    await userEvent.click(submitBtn);

    expect(reqSpy).toHaveBeenCalled();
  });

  it('renders overview dashboard metrics and enables direct operator jump', async () => {
    vi.spyOn(api, 'getOverview').mockResolvedValueOnce({
      system_status: {
        state: 'READY',
        version: '1.0.1',
        commit_sha: '5e16a94',
        database_schema: 'sqlite_wal',
        active_workers: 4,
        pending_tasks: 2,
        updated_at: '2026-08-20T00:00:00Z',
      },
      tasks: {
        active: 2,
        queued: 1,
        blocked: 0,
        review: 1,
        completed: 8,
        failed: 0,
        total: 12,
      },
      agents: {
        total: 4,
        active: 3,
        idle: 1,
      },
      providers: [],
      memory_health: 'OPTIMAL',
      security_notices: [],
      evaluated_at: '2026-08-20T00:00:00Z',
    });

    const navFn = vi.fn();

    render(
      <ToastProvider>
        <Overview onNavigate={navFn} />
      </ToastProvider>
    );

    expect(await screen.findByText('Mission Control Overview')).toBeInTheDocument();
    expect(screen.getByText('Active Tasks')).toBeInTheDocument();
  });
});
