import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Operations } from './Operations';
import { api } from '../api/client';

const mockDoctorData = {
  overall_status: 'READY',
  checks: [
    {
      component: 'database_sqlite',
      status: 'READY',
      latency_ms: 1,
      message: 'SQLite WAL mode active with safe foreign keys',
    },
    {
      component: 'sandbox_isolation',
      status: 'READY',
      latency_ms: 0,
      message: 'Bubblewrap / rootless cgroups active',
    },
  ],
  evaluated_at: '2026-08-20T00:00:00Z',
};

const mockResources = {
  cpu: { model: 'Fixture CPU', logical: 8, effective: 4, architecture: 'amd64', source: '/proc/cpuinfo' },
  memory: { total_bytes: 16 * 1073741824, available_bytes: 8 * 1073741824, swap_total_bytes: 0, swap_used_bytes: 0 },
  storage: { path: '/tmp', total_bytes: 100 * 1073741824, free_bytes: 60 * 1073741824 },
  accelerators: [],
  ollama: { status: 'NOT_AVAILABLE', endpoint: 'http://127.0.0.1:11434', models: [] },
  health: { overall: 'OK', ram: 'OK', swap: 'UNKNOWN', disk: 'OK', thermal: 'UNKNOWN' },
  recommendation: { concurrency: 2, profile: 'Safe', reasons: ['fixture'] },
  collected_at: '2026-08-20T00:00:00Z',
};

describe('Operations Route (T209)', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    vi.spyOn(api, 'listBackups').mockResolvedValue({ backups: [], total_count: 0 });
    vi.spyOn(api, 'listMaintenanceJobs').mockResolvedValue({ jobs: [], total_count: 0 });
    vi.spyOn(api, 'getReleaseTrust').mockResolvedValue({
      binary_commit_sha: 'abc1234',
      source_repo: 'BlackArch/marshal',
      pack_manifest_status: 'verified',
      pack_manifest_digest: 'sha256:1111',
      sbom_status: 'valid',
      sbom_format: 'CycloneDX',
      signing_status: 'signed',
      signer_identity: 'Zen1th53',
      reproducibility_status: 'bit-exact',
      artifacts: [],
      evaluated_at: '2026-08-20T00:00:00Z',
    });
  });

  it('renders system health overview and diagnostic checks', async () => {
    vi.spyOn(api, 'getDoctorReport').mockResolvedValueOnce(mockDoctorData);
    vi.spyOn(api, 'getResources').mockResolvedValueOnce(mockResources);

    render(<Operations />);

    expect(await screen.findByText('System Health & Doctor Diagnostics')).toBeInTheDocument();
    expect(screen.getByText('MARSHAL Core Engine: READY')).toBeInTheDocument();
    expect(screen.getByText('database_sqlite')).toBeInTheDocument();
    expect(screen.getByText('sandbox_isolation')).toBeInTheDocument();
  });

  it('refreshes doctor report on button click', async () => {
    const doctorSpy = vi.spyOn(api, 'getDoctorReport').mockResolvedValue(mockDoctorData);
    vi.spyOn(api, 'getResources').mockResolvedValue(mockResources);

    render(<Operations />);

    const refreshBtn = await screen.findByRole('button', { name: /Run Doctor Diagnostics/i });
    await userEvent.click(refreshBtn);

    expect(doctorSpy).toHaveBeenCalled();
  });
});
