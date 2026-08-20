import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MaintenanceWorkspace } from './MaintenanceWorkspace';
import { ToastProvider } from '../../../components/toast';
import { api } from '../../../api/client';

const mockJobsData = {
  jobs: [
    {
      job_id: 'JOB-GC-001',
      job_type: 'worktree_gc',
      status: 'completed',
      is_dry_run: false,
      target_scope: 'ephemeral_worktrees',
      reclaimed_bytes: 52428800,
      records_affected: 6,
      audit_id: 'AUD-MAINT-GC-001',
      started_at: '2026-08-20T00:00:00Z',
    },
  ],
  total_count: 1,
};

describe('MaintenanceWorkspace Component (T211)', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('renders maintenance job history and controls', async () => {
    vi.spyOn(api, 'listMaintenanceJobs').mockResolvedValueOnce(mockJobsData);

    render(
      <ToastProvider>
        <MaintenanceWorkspace />
      </ToastProvider>
    );

    expect(await screen.findByText('JOB-GC-001')).toBeInTheDocument();
    expect(screen.getByText('WORKTREE GC')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Simulate Dry Run/i })).toBeInTheDocument();
  });

  it('triggers dry run simulation on button click', async () => {
    vi.spyOn(api, 'listMaintenanceJobs').mockResolvedValue(mockJobsData);
    const jobSpy = vi.spyOn(api, 'createMaintenanceJob').mockResolvedValueOnce({
      job_id: 'JOB-DRY-001',
      job_type: 'worktree_gc',
      status: 'dry_run_ready',
      is_dry_run: true,
      target_scope: 'ephemeral_worktrees',
      reclaimed_bytes: 31457280,
      records_affected: 3,
      audit_id: 'AUD-DRY-001',
      started_at: '2026-08-20T00:00:00Z',
    });

    render(
      <ToastProvider>
        <MaintenanceWorkspace />
      </ToastProvider>
    );

    const runBtn = await screen.findByRole('button', { name: /Simulate Dry Run/i });
    await userEvent.click(runBtn);

    expect(jobSpy).toHaveBeenCalledWith({
      job_type: 'worktree_gc',
      is_dry_run: true,
      target_scope: 'ephemeral_worktrees',
    });
  });
});
