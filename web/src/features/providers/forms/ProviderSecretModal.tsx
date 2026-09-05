import { useState } from 'react';
import { api } from '../../../api/client';
import { Button } from '../../../components/ui';
import { ErrorState } from '../../../components/state';
import type { ProviderDTO } from '../../../api/types';

interface ProviderSecretModalProps {
  provider: ProviderDTO;
  isOpen: boolean;
  onClose: () => void;
  onUpdated: () => void;
}

export function ProviderSecretModal({ provider, isOpen, onClose, onUpdated }: ProviderSecretModalProps) {
  const [secretKey, setSecretKey] = useState('');
  const [envVar, setEnvVar] = useState(provider.secret_ref.ref_name || '');
  const [version, setVersion] = useState(provider.secret_ref.version || '1');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  if (!isOpen) return null;

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!secretKey.trim() && !envVar.trim()) {
      setError('Secret key or reference name is required');
      return;
    }
    setSubmitting(true);
    setError(null);

    try {
      await api.setProviderSecret(provider.id, {
        secret_key: secretKey.trim() || undefined,
        env_var: envVar.trim() || undefined,
        version: version.trim() || '1',
      });
      onUpdated();
      onClose();
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to configure provider SecretRef');
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="modal-backdrop" onClick={onClose} role="dialog" aria-modal="true" aria-label="Configure SecretRef">
      <div className="modal-card" onClick={(e) => e.stopPropagation()} style={{ maxWidth: '520px' }}>
        <div className="modal-header">
          <div>
            <h3 className="modal-title">SecretRef Management: {provider.name}</h3>
            <code className="agent-id-badge">{provider.id}</code>
          </div>
          <button type="button" className="btn btn-ghost btn-sm" onClick={onClose} aria-label="Close">
            ✕
          </button>
        </div>

        <form onSubmit={handleSubmit}>
          <div className="modal-body" style={{ display: 'flex', flexDirection: 'column', gap: '1rem' }}>
            {error && <ErrorState severity="error" message={error} />}

            <div style={{ padding: '0.75rem', background: 'rgba(59, 130, 246, 0.1)', border: '1px solid rgba(59, 130, 246, 0.3)', borderRadius: '6px', fontSize: '0.8125rem' }}>
              <strong>🔒 Write-Only Security Boundary</strong>
              <p style={{ margin: '0.25rem 0 0', color: 'var(--color-text-muted, #94a3b8)' }}>
                MARSHAL enforces zero secret leakage. Raw credentials are never stored in plain responses or exposed in browser memory.
              </p>
            </div>

            <div className="form-group" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '0.5rem 0.75rem', background: 'var(--color-surface, #1e293b)', borderRadius: '6px' }}>
              <span className="meta-label">Current Status:</span>
              <span className="font-mono text-xs" style={{ color: provider.secret_ref.configured ? '#10b981' : '#f59e0b', fontWeight: 600 }}>
                {provider.secret_ref.configured ? '✓ CONFIGURED' : '✕ NOT CONFIGURED'}
              </span>
            </div>

            <div className="form-group">
              <label className="form-label" htmlFor="secret-ref-name">SecretRef Identifier</label>
              <input
                id="secret-ref-name"
                type="text"
                className="filter-input font-mono"
                value={envVar}
                onChange={(e) => setEnvVar(e.target.value)}
                placeholder="sec-provider-auth"
              />
            </div>

            <div className="form-group">
              <label className="form-label" htmlFor="secret-key-input">New API Key / Token (Write-Only)</label>
              <input
                id="secret-key-input"
                type="password"
                className="filter-input font-mono"
                placeholder="Paste new secret key (leaves unchanged if blank)…"
                value={secretKey}
                onChange={(e) => setSecretKey(e.target.value)}
                autoComplete="new-password"
              />
            </div>

            <div className="form-group">
              <label className="form-label" htmlFor="secret-version">SecretRef Version</label>
              <input
                id="secret-version"
                type="text"
                className="filter-input font-mono"
                value={version}
                onChange={(e) => setVersion(e.target.value)}
                placeholder="1"
              />
            </div>
          </div>

          <div className="modal-actions" style={{ marginTop: '1.5rem' }}>
            <Button variant="secondary" size="sm" type="button" onClick={onClose} disabled={submitting}>
              Cancel
            </Button>
            <Button variant="primary" size="sm" type="submit" disabled={submitting}>
              {submitting ? 'Updating…' : 'Update SecretRef'}
            </Button>
          </div>
        </form>
      </div>
    </div>
  );
}
