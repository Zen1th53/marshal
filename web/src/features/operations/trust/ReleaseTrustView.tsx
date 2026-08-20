import { useState, useEffect, useCallback } from 'react';
import { api } from '../../../api/client';
import { StatusBadge, Button } from '../../../components/ui';
import { LoadingState, ErrorState } from '../../../components/state';

interface TrustArtifact {
  name: string;
  digest_sha256: string;
  size_bytes: number;
  download_path: string;
}

interface ReleaseTrustData {
  binary_commit_sha: string;
  source_repo: string;
  pack_manifest_status: string;
  pack_manifest_digest: string;
  sbom_status: string;
  sbom_format: string;
  signing_status: string;
  signer_identity: string;
  reproducibility_status: string;
  artifacts: TrustArtifact[];
  evaluated_at: string;
}

export function ReleaseTrustView() {
  const [data, setData] = useState<ReleaseTrustData | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const fetchTrust = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const resp = await api.getReleaseTrust();
      setData(resp);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to query release trust metadata');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void fetchTrust();
  }, [fetchTrust]);

  return (
    <div className="release-trust-container" style={{ marginTop: 'var(--space-5)' }}>
      <div className="flex-row items-center justify-between" style={{ marginBottom: 'var(--space-3)' }}>
        <div>
          <h3 className="text-base font-semibold">Release Trust, SBOM & Cryptographic Provenance</h3>
          <span className="text-xs text-dim">
            Cosign PKI signature verification, CycloneDX SBOM catalog, and reproducible build digest transparency
          </span>
        </div>
      </div>

      {loading ? (
        <LoadingState message="Auditing binary commit signature, CycloneDX SBOM, and pack manifest integrity…" />
      ) : error ? (
        <ErrorState severity="error" message={error} onRetry={fetchTrust} />
      ) : data ? (
        <div className="trust-content">
          {/* Trust Metadata Grid */}
          <div className="task-meta-grid" style={{ marginBottom: 'var(--space-4)' }}>
            <div className="meta-box">
              <span className="meta-label">Cosign PKI Signature</span>
              <div className="flex-row items-center gap-2">
                <StatusBadge
                  status={data.signing_status === 'COSIGN_PKI_VERIFIED' ? 'ready' : 'degraded'}
                  label={data.signing_status}
                />
                <span className="font-mono text-xs text-dim">{data.signer_identity}</span>
              </div>
            </div>

            <div className="meta-box">
              <span className="meta-label">Pack Manifest</span>
              <div className="flex-row items-center gap-2">
                <StatusBadge
                  status={data.pack_manifest_status === 'VERIFIED_PASS' ? 'ready' : 'degraded'}
                  label={data.pack_manifest_status}
                />
                <span className="font-mono text-xs text-dim">
                  {data.pack_manifest_digest.slice(0, 12)}…
                </span>
              </div>
            </div>

            <div className="meta-box">
              <span className="meta-label">Software Bill of Materials (SBOM)</span>
              <div className="flex-row items-center gap-2">
                <StatusBadge
                  status={data.sbom_status === 'GENERATED_AVAILABLE' ? 'ready' : 'pending'}
                  label={data.sbom_format}
                />
              </div>
            </div>

            <div className="meta-box">
              <span className="meta-label">Reproducibility</span>
              <span className="meta-value font-mono text-xs">{data.reproducibility_status}</span>
            </div>
          </div>

          {/* Trust Artifacts Table */}
          <div className="table-responsive">
            <table className="data-table" aria-label="Trust Artifacts Table">
              <thead>
                <tr>
                  <th>Artifact</th>
                  <th>SHA-256 Digest</th>
                  <th>Size</th>
                  <th>Action</th>
                </tr>
              </thead>
              <tbody>
                {data.artifacts.map((art) => (
                  <tr key={art.name}>
                    <td>
                      <code className="font-mono text-xs font-bold">{art.name}</code>
                    </td>
                    <td>
                      <code className="font-mono text-xs text-dim">
                        {art.digest_sha256}
                      </code>
                    </td>
                    <td className="font-mono text-xs">{(art.size_bytes / 1024).toFixed(1)} KB</td>
                    <td>
                      <Button variant="ghost" size="sm" onClick={() => window.open(art.download_path, '_blank')}>
                        Download
                      </Button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      ) : null}
    </div>
  );
}
