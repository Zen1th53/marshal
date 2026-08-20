import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemorySecurityHealth } from './MemorySecurityHealth';
import { api } from '../../../api/client';

const mockHealthData = {
  encryption_status: 'aes_256_gcm_active',
  key_id: 'KEY-MARSHAL-2026-PRIMARY',
  integrity_status: 'verified_clean',
  verified_records: 24,
  tampered_records: 0,
  rebuild_watermark: 24,
  indexes: [
    {
      name: 'lexical_bm25',
      generation: 4,
      status: 'healthy',
      outbox_lag_ms: 4,
      records_indexed: 24,
    },
  ],
  acl_matrix: [
    {
      scope: 'project',
      enforcement_mode: 'strict_rbfa',
      read_isolation: 'authenticated_agents',
      write_authority: 'quorum_or_lead_only',
    },
  ],
  evaluated_at: '2026-08-20T00:00:00Z',
};

describe('MemorySecurityHealth Component (T208)', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('renders encryption, integrity, index and ACL health', async () => {
    vi.spyOn(api, 'getMemorySecurityHealth').mockResolvedValueOnce(mockHealthData);

    render(<MemorySecurityHealth onClose={vi.fn()} />);

    expect(await screen.findByText('Memory Security, Encryption, ACL & Index Health')).toBeInTheDocument();
    expect(screen.getByText('AES-256-GCM')).toBeInTheDocument();
    expect(screen.getByText('lexical_bm25')).toBeInTheDocument();
    expect(screen.getByText('strict_rbfa')).toBeInTheDocument();
  });

  it('calls onClose when close button is clicked', async () => {
    vi.spyOn(api, 'getMemorySecurityHealth').mockResolvedValue(mockHealthData);
    const closeFn = vi.fn();

    render(<MemorySecurityHealth onClose={closeFn} />);
    const closeBtn = await screen.findByLabelText('Close');
    await userEvent.click(closeBtn);

    expect(closeFn).toHaveBeenCalled();
  });
});
