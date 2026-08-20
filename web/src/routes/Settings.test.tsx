import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Settings } from './Settings';
import { ToastProvider } from '../components/toast';
import { api } from '../api/client';

const mockSettingsData = {
  revision: 1,
  system_mode: 'strict',
  max_concurrent_workers: 4,
  telemetry_level: 'standard',
  auto_consolidation_enabled: true,
  memory_retention_days: 30,
  requires_restart: false,
  env_diagnostics: {
    os_arch: 'linux/amd64',
    go_version: 'go1.24',
    sandbox_kind: 'bubblewrap_rootless',
    storage_engine: 'sqlite_wal_canonical',
  },
  updated_at: '2026-08-20T00:00:00Z',
};

describe('Settings Route (T214)', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('renders settings fields and environment diagnostics', async () => {
    vi.spyOn(api, 'getSettings').mockResolvedValueOnce(mockSettingsData);

    render(
      <ToastProvider>
        <Settings />
      </ToastProvider>
    );

    expect(await screen.findByText('System Settings & Environment Diagnostics')).toBeInTheDocument();
    expect(screen.getByText('linux/amd64')).toBeInTheDocument();
    expect(screen.getByText('go1.24')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Save System Settings/i })).toBeInTheDocument();
  });

  it('submits updated settings with expected revision', async () => {
    vi.spyOn(api, 'getSettings').mockResolvedValue(mockSettingsData);
    const updateSpy = vi.spyOn(api, 'updateSettings').mockResolvedValueOnce({
      ...mockSettingsData,
      revision: 2,
      requires_restart: true,
    });

    render(
      <ToastProvider>
        <Settings />
      </ToastProvider>
    );

    const saveBtn = await screen.findByRole('button', { name: /Save System Settings/i });
    await userEvent.click(saveBtn);

    expect(updateSpy).toHaveBeenCalledWith({
      expected_revision: 1,
      system_mode: 'strict',
      max_concurrent_workers: 4,
      telemetry_level: 'standard',
      auto_consolidation_enabled: true,
      memory_retention_days: 30,
    });
  });
});
