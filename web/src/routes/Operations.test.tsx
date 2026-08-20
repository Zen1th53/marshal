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

describe('Operations Route (T209)', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('renders system health overview and diagnostic checks', async () => {
    vi.spyOn(api, 'getDoctorReport').mockResolvedValueOnce(mockDoctorData);

    render(<Operations />);

    expect(await screen.findByText('System Health & Doctor Diagnostics')).toBeInTheDocument();
    expect(screen.getByText('MARSHAL Core Engine: READY')).toBeInTheDocument();
    expect(screen.getByText('database_sqlite')).toBeInTheDocument();
    expect(screen.getByText('sandbox_isolation')).toBeInTheDocument();
  });

  it('refreshes doctor report on button click', async () => {
    const doctorSpy = vi.spyOn(api, 'getDoctorReport').mockResolvedValue(mockDoctorData);

    render(<Operations />);

    const refreshBtn = await screen.findByRole('button', { name: /Run Doctor Diagnostics/i });
    await userEvent.click(refreshBtn);

    expect(doctorSpy).toHaveBeenCalled();
  });
});
