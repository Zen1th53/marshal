import { useState } from 'react';
import { api } from '../../../api/client';
import { Button } from '../../../components/ui';
import { ErrorState } from '../../../components/state';
import type { ProviderDTO } from '../../../api/types';

interface ConfigureProviderModalProps {
  provider: ProviderDTO;
  isOpen: boolean;
  onClose: () => void;
  onUpdated: () => void;
}

export function ConfigureProviderModal({ provider, isOpen, onClose, onUpdated }: ConfigureProviderModalProps) {
  const [enabled, setEnabled] = useState(provider.enabled);
  const [endpointUrl, setEndpointUrl] = useState(provider.endpoint_url || '');
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  if (!isOpen) return null;

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setSubmitting(true);
    setError(null);

    try {
      await api.updateProvider(provider.id, {
        enabled,
        endpoint_url: endpointUrl.trim() || undefined,
      });
      onUpdated();
      onClose();
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to update provider configuration');
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="modal-backdrop" onClick={onClose} role="dialog" aria-modal="true" aria-label="Configure Provider">
      <div className="modal-card" onClick={(e) => e.stopPropagation()} style={{ maxWidth: '520px' }}>
        <div className="modal-header">
          <div>
            <h3 className="modal-title">Configure Provider: {provider.name}</h3>
            <code className="agent-id-badge">{provider.id}</code>
          </div>
          <button type="button" className="btn btn-ghost btn-sm" onClick={onClose} aria-label="Close">
            ✕
          </button>
        </div>

        <form onSubmit={handleSubmit}>
          <div className="modal-body" style={{ display: 'flex', flexDirection: 'column', gap: '1rem' }}>
            {error && <ErrorState severity="error" message={error} />}

            <div className="form-group" style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '0.75rem', background: 'var(--color-surface, #1e293b)', borderRadius: '6px' }}>
              <div>
                <strong>Enable Provider Fleet Integration</strong>
                <p style={{ margin: 0, fontSize: '0.75rem', color: 'var(--color-text-muted, #94a3b8)' }}>
                  When enabled, this provider participates in multi-agent routing.
                </p>
              </div>
              <input
                type="checkbox"
                checked={enabled}
                onChange={(e) => setEnabled(e.target.checked)}
                style={{ width: '1.25rem', height: '1.25rem', cursor: 'pointer' }}
              />
            </div>

            <div className="form-group">
              <label className="form-label" htmlFor="provider-endpoint">
                Custom Endpoint / Base URL {provider.class === 'local' ? '(e.g. http://127.0.0.1:11434)' : '(Optional override)'}
              </label>
              <input
                id="provider-endpoint"
                type="url"
                className="filter-input font-mono"
                placeholder={provider.class === 'local' ? 'http://127.0.0.1:11434' : 'https://api.anthropic.com'}
                value={endpointUrl}
                onChange={(e) => setEndpointUrl(e.target.value)}
              />
            </div>

            <div className="form-group">
              <span className="meta-label">Configured Models</span>
              <div style={{ display: 'flex', flexDirection: 'column', gap: '0.25rem', marginTop: '0.25rem' }}>
                {provider.models.map((m) => (
                  <div key={m.id} style={{ display: 'flex', justifyContent: 'space-between', fontSize: '0.8125rem', padding: '0.25rem 0.5rem', background: 'rgba(255,255,255,0.03)', borderRadius: '4px' }}>
                    <code className="font-mono">{m.id}</code>
                    <span style={{ color: 'var(--color-text-muted, #94a3b8)' }}>{Math.round(m.context_window / 1000)}k context</span>
                  </div>
                ))}
              </div>
            </div>
          </div>

          <div className="modal-actions" style={{ marginTop: '1.5rem' }}>
            <Button variant="secondary" size="sm" type="button" onClick={onClose} disabled={submitting}>
              Cancel
            </Button>
            <Button variant="primary" size="sm" type="submit" disabled={submitting}>
              {submitting ? 'Saving…' : 'Save Configuration'}
            </Button>
          </div>
        </form>
      </div>
    </div>
  );
}
