import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { BackupRestoreWorkspace } from './BackupRestoreWorkspace';
import { ToastProvider } from '../../../components/toast';
import { api } from '../../../api/client';

const mockBackupsData = {
  backups: [
    {
      backup_id: 'BKP-20260820-001',
      schema_version: 1,
      size_bytes: 1048576,
      digest_sha256: 'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855',
      status: 'verified',
      created_at: '2026-08-20T00:00:00Z',
    },
  ],
  total_count: 1,
};

describe('BackupRestoreWorkspace Component (T210)', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('renders state backups list and actions', async () => {
    vi.spyOn(api, 'listBackups').mockResolvedValueOnce(mockBackupsData);

    render(
      <ToastProvider>
        <BackupRestoreWorkspace />
      </ToastProvider>
    );

    expect(await screen.findByText('BKP-20260820-001')).toBeInTheDocument();
    expect(screen.getByText('VERIFIED')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Create State Backup/i })).toBeInTheDocument();
  });

  it('opens restore confirmation modal with safety notice', async () => {
    vi.spyOn(api, 'listBackups').mockResolvedValue(mockBackupsData);

    render(
      <ToastProvider>
        <BackupRestoreWorkspace />
      </ToastProvider>
    );

    const restoreBtn = await screen.findByRole('button', { name: /Restore/i });
    await userEvent.click(restoreBtn);

    expect(await screen.findByText('Confirm State Restoration')).toBeInTheDocument();
    expect(screen.getByText(/A pre-restore safety snapshot will automatically be created/i)).toBeInTheDocument();
  });
});
