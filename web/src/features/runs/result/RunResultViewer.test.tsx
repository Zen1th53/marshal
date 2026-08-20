import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { RunResultViewer } from './RunResultViewer';
import { ToastProvider } from '../../../components/toast';
import { api } from '../../../api/client';

const mockRunResultData = {
  run_id: 'RUN-001',
  base_commit: '1a2b3c4',
  head_commit: '5e6f7g8',
  files_summary: [
    {
      path: 'internal/webcontrol/runs.go',
      status: 'added',
      insertions: 98,
      deletions: 0,
    },
  ],
  artifacts: [
    {
      id: 'art-001',
      name: 'benchmark_results.json',
      sha256: 'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855',
      size_bytes: 45,
      content_type: 'application/json',
    },
  ],
  worktree_status: 'retained',
  checkpoint_id: 'chk-RUN-001',
  can_recover: true,
  created_at: '2026-08-20T00:00:00Z',
};

describe('RunResultViewer (T190)', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('renders modified files diff summary and generated artifacts table', async () => {
    vi.spyOn(api, 'getRunResult').mockResolvedValueOnce(mockRunResultData);

    render(
      <ToastProvider>
        <RunResultViewer runId="RUN-001" />
      </ToastProvider>
    );

    expect(await screen.findByText('Modified Codebase Files (1)')).toBeInTheDocument();
    expect(screen.getByText('internal/webcontrol/runs.go')).toBeInTheDocument();
    expect(screen.getByText('+98')).toBeInTheDocument();
    expect(screen.getByText('benchmark_results.json')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /Download artifact benchmark_results.json/i })).toHaveAttribute(
      'href',
      '/api/v1/artifacts/art-001/download'
    );
  });

  it('handles checkpoint restoration action', async () => {
    vi.spyOn(api, 'getRunResult').mockResolvedValueOnce(mockRunResultData);
    const recoverSpy = vi.spyOn(api, 'recoverRun').mockResolvedValueOnce({
      run_id: 'RUN-001',
      checkpoint_id: 'chk-RUN-001',
      recovered_at: '2026-08-20T00:00:00Z',
      status: 'restored',
    });

    render(
      <ToastProvider>
        <RunResultViewer runId="RUN-001" />
      </ToastProvider>
    );

    const restoreBtn = await screen.findByRole('button', { name: /Restore Checkpoint/i });
    await userEvent.click(restoreBtn);

    expect(recoverSpy).toHaveBeenCalledWith('RUN-001');
  });
});
