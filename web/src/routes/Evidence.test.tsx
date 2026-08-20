import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Evidence } from './Evidence';
import { api } from '../api/client';

const mockEvidenceData = {
  items: [
    {
      id: 'EVID-002-MERKLE',
      task_id: 'TASK-002-CONTROL-PLANE',
      run_id: 'RUN-TASK-002-01',
      type: 'merkle_proof',
      producer: 'agent-codex-implementer',
      digest: 'ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb',
      size_bytes: 2048,
      integrity_status: 'verified',
      created_at: '2026-08-20T00:00:00Z',
    },
  ],
  total_count: 1,
  limit: 50,
  offset: 0,
};

const mockEvidenceDetail = {
  id: 'EVID-002-MERKLE',
  task_id: 'TASK-002-CONTROL-PLANE',
  run_id: 'RUN-TASK-002-01',
  type: 'merkle_proof',
  producer: 'agent-codex-implementer',
  digest: 'ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb',
  calculated_digest: 'ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb',
  integrity_status: 'verified',
  artifact_id: 'art-001',
  signature: 'ed25519-sig-test-12345678',
  payload: {
    assertions: ['gate_assertion: zero security boundary violations'],
  },
  created_at: '2026-08-20T00:00:00Z',
};

describe('Evidence Explorer (T194)', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('renders evidence list with digest and integrity badges', async () => {
    vi.spyOn(api, 'listEvidence').mockResolvedValueOnce(mockEvidenceData);

    render(<Evidence />);
    expect(await screen.findByText('Cryptographic Evidence & Proofs')).toBeInTheDocument();
    expect(screen.getByText('EVID-002-MERKLE')).toBeInTheDocument();
    expect(screen.getByText('MERKLE PROOF')).toBeInTheDocument();
    expect(screen.getByText('agent-codex-implementer')).toBeInTheDocument();
  });

  it('opens detail inspector for evidence on Inspect click', async () => {
    vi.spyOn(api, 'listEvidence').mockResolvedValueOnce(mockEvidenceData);
    vi.spyOn(api, 'getEvidenceDetail').mockResolvedValueOnce(mockEvidenceDetail);

    render(<Evidence />);
    expect(await screen.findByText('EVID-002-MERKLE')).toBeInTheDocument();

    const inspectBtn = screen.getByRole('button', { name: /Inspect Proof/i });
    await userEvent.click(inspectBtn);

    expect(await screen.findByText('Integrity Proof Details')).toBeInTheDocument();
    expect(screen.getByText(/ed25519-sig-test-12345678/i)).toBeInTheDocument();
    expect(screen.getByText(/gate_assertion: zero security boundary violations/i)).toBeInTheDocument();
  });
});
