import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import { ReleaseTrustView } from './ReleaseTrustView';
import { api } from '../../../api/client';

const mockTrustData = {
  binary_commit_sha: '7550994',
  source_repo: 'github.com/Zen1th53/marshal',
  pack_manifest_status: 'VERIFIED_PASS',
  pack_manifest_digest: '978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb22',
  sbom_status: 'GENERATED_AVAILABLE',
  sbom_format: 'CycloneDX JSON 1.5',
  signing_status: 'COSIGN_PKI_VERIFIED',
  signer_identity: 'extreme29@proton.me',
  reproducibility_status: 'REPRODUCIBLE_BIT_EXACT',
  artifacts: [
    {
      name: 'PACK-MANIFEST.json',
      digest_sha256: '978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb22',
      size_bytes: 8192,
      download_path: '/distribution/PACK-MANIFEST.json',
    },
  ],
  evaluated_at: '2026-08-20T00:00:00Z',
};

describe('ReleaseTrustView Component (T213)', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('renders Cosign PKI, Pack Manifest, and SBOM trust metadata', async () => {
    vi.spyOn(api, 'getReleaseTrust').mockResolvedValueOnce(mockTrustData);

    render(<ReleaseTrustView />);

    expect(await screen.findByText('Release Trust, SBOM & Cryptographic Provenance')).toBeInTheDocument();
    expect(screen.getByText('COSIGN_PKI_VERIFIED')).toBeInTheDocument();
    expect(screen.getByText('extreme29@proton.me')).toBeInTheDocument();
    expect(screen.getByText('CycloneDX JSON 1.5')).toBeInTheDocument();
    expect(screen.getByText('PACK-MANIFEST.json')).toBeInTheDocument();
  });
});
