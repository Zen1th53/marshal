import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryVersioningWorkspace } from './MemoryVersioningWorkspace';
import { ToastProvider } from '../../../components/toast';
import { api } from '../../../api/client';

const mockSnapshotData = {
  snapshots: [
    {
      snapshot_id: 'SNAP-001-INIT',
      branch: 'main',
      manifest_digest_sha256: '7f83b1657ff1fc53b92dc18148a1d65dfc2d4b1fa3d677284addd200126d9069',
      record_count: 14,
      message: 'Baseline corpus post loopback initialization',
      created_by: 'operator',
      created_at: '2026-08-20T00:00:00Z',
    },
    {
      snapshot_id: 'SNAP-002-QUORUM',
      branch: 'main',
      manifest_digest_sha256: '9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08',
      record_count: 18,
      message: 'Attested multi-agent quorum procedures',
      created_by: 'operator',
      created_at: '2026-08-20T01:00:00Z',
    },
  ],
  active_head: 'SNAP-002-QUORUM',
  total_count: 2,
};

const mockDiffData = {
  from_snapshot: 'SNAP-001-INIT',
  to_snapshot: 'SNAP-002-QUORUM',
  entries: [
    {
      memory_id: 'MEM-002-QUORUM-SPEC',
      change_type: 'added',
      new_title: 'Independent Multi-Agent Quorum Verification',
      details: 'Added 2-of-3 quorum consensus protocol rule',
    },
  ],
  has_conflict: false,
};

describe('MemoryVersioningWorkspace Component (T206)', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('renders snapshot history and highlights active head', async () => {
    vi.spyOn(api, 'listMemorySnapshots').mockResolvedValueOnce(mockSnapshotData);

    render(
      <ToastProvider>
        <MemoryVersioningWorkspace />
      </ToastProvider>
    );

    expect(await screen.findByText('SNAP-001-INIT')).toBeInTheDocument();
    expect(screen.getByText('SNAP-002-QUORUM')).toBeInTheDocument();
    expect(screen.getByText('HEAD')).toBeInTheDocument();
  });

  it('opens diff modal when Compare Head Diff is clicked', async () => {
    vi.spyOn(api, 'listMemorySnapshots').mockResolvedValue(mockSnapshotData);
    vi.spyOn(api, 'getMemorySnapshotDiff').mockResolvedValueOnce(mockDiffData);

    render(
      <ToastProvider>
        <MemoryVersioningWorkspace />
      </ToastProvider>
    );

    const diffBtn = await screen.findByRole('button', { name: /Compare Head Diff/i });
    await userEvent.click(diffBtn);

    expect(await screen.findByText('Memory Snapshot Diff Breakdown')).toBeInTheDocument();
    expect(screen.getByText('MEM-002-QUORUM-SPEC')).toBeInTheDocument();
    expect(screen.getByText('Added 2-of-3 quorum consensus protocol rule')).toBeInTheDocument();
  });
});
