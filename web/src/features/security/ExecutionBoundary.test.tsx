import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import { ExecutionBoundary } from './ExecutionBoundary';
import { api } from '../../api/client';

const mockBoundaryData = {
  run_id: 'RUN-001',
  sandbox_backend: 'bubblewrap',
  backend_status: 'enforced',
  network_policy: 'blocked',
  is_network_isolated: true,
  cpu_quota_pct: 100,
  memory: {
    limit: 2048,
    used: 412,
    unit: 'MB',
    usage_pct: 20.1,
  },
  pids: {
    limit: 64,
    used: 8,
    unit: 'PIDs',
    usage_pct: 12.5,
  },
  disk: {
    limit: 5120,
    used: 840,
    unit: 'MB',
    usage_pct: 16.4,
  },
  was_oom_killed: false,
  was_pid_exhausted: false,
  was_disk_exhausted: false,
  mounted_paths: ['/workspace (rw, nodev, nosuid)'],
  audited_at: '2026-08-20T00:00:00Z',
};

describe('ExecutionBoundary Component (T198)', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('renders sandbox backend, network isolation status, and resource meters', async () => {
    vi.spyOn(api, 'getRunBoundary').mockResolvedValueOnce(mockBoundaryData);

    render(<ExecutionBoundary runId="RUN-001" />);
    expect(await screen.findByText('BUBBLEWRAP')).toBeInTheDocument();
    expect(screen.getByText('AIRGAPPED / ISOLATED')).toBeInTheDocument();
    expect(screen.getByText(/412 \/ 2048 MB/i)).toBeInTheDocument();
    expect(screen.getByText('/workspace (rw, nodev, nosuid)')).toBeInTheDocument();
  });

  it('renders OOM warning badge if was_oom_killed is true', async () => {
    vi.spyOn(api, 'getRunBoundary').mockResolvedValueOnce({
      ...mockBoundaryData,
      was_oom_killed: true,
    });

    render(<ExecutionBoundary runId="RUN-001" />);
    expect(await screen.findByText(/PROCESS TERMINATED DUE TO OOM/i)).toBeInTheDocument();
  });
});
