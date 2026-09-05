import { useState, useEffect, useCallback } from 'react';
import { api } from '../api/client';
import { StatusBadge, Button } from '../components/ui';
import { LoadingState, ErrorState } from '../components/state';
import { useToast } from '../components/toast';
import { ConfigureProviderModal } from '../features/providers/forms/ConfigureProviderModal';
import { ProviderSecretModal } from '../features/providers/forms/ProviderSecretModal';
import type { ProviderDTO, RouterDecisionDTO, ProviderInventoryResponseDTO } from '../api/types';

export function Providers() {
  const [data, setData] = useState<ProviderInventoryResponseDTO | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [probingProviderId, setProbingProviderId] = useState<string | null>(null);
  const [selectedConfigProvider, setSelectedConfigProvider] = useState<ProviderDTO | null>(null);
  const [selectedSecretProvider, setSelectedSecretProvider] = useState<ProviderDTO | null>(null);
  const { addToast } = useToast();

  const fetchProviders = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const resp = await api.getProviders();
      setData(resp);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to query provider registry');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void fetchProviders();
  }, [fetchProviders]);

  const handleProbeSingle = async (providerId: string) => {
    setProbingProviderId(providerId);
    try {
      const res = await api.probeProvider(providerId);
      addToast({
        type: 'success',
        message: `Provider ${providerId} probed: ${res.probe_status.toUpperCase()}`,
      });
      await fetchProviders();
    } catch (err: unknown) {
      addToast({
        type: 'error',
        message: err instanceof Error ? err.message : `Failed to probe provider ${providerId}`,
      });
    } finally {
      setProbingProviderId(null);
    }
  };

  const handleTogglePin = async (decision: RouterDecisionDTO) => {
    try {
      await api.overrideRouter({
        intent: decision.intent,
        model_id: decision.selected_model,
        is_pinned: !decision.is_pinned,
      });
      addToast({
        type: 'success',
        message: decision.is_pinned
          ? `Unpinned routing for intent "${decision.intent}". Dynamic routing restored.`
          : `Pinned intent "${decision.intent}" to model ${decision.selected_model}.`,
      });
      await fetchProviders();
    } catch (err: unknown) {
      addToast({
        type: 'error',
        message: err instanceof Error ? err.message : 'Failed to update routing override',
      });
    }
  };

  if (loading && !data) return <LoadingState message="Probing provider endpoints & evaluating model routing…" />;
  if (error && !data) return <ErrorState severity="error" message={error} onRetry={fetchProviders} />;
  if (!data) return null;

  return (
    <div className="providers-container">
      <div className="providers-header">
        <div className="providers-headline">
          <h2 className="providers-title">Model Provider Fleet & Router Matrix</h2>
          <span className="providers-subtitle">
            Zero credential exposure • Transparent multi-agent routing rationale
          </span>
        </div>
        <Button variant="secondary" size="sm" onClick={fetchProviders}>
          Probe All Providers
        </Button>
      </div>

      {/* Provider Fleet Cards */}
      <div className="providers-grid">
        {data.providers.map((p) => (
          <div key={p.id} className="provider-card">
            <div className="provider-card-header">
              <div className="provider-name-group">
                <h3 className="provider-name">{p.name}</h3>
                <div style={{ display: 'flex', gap: '0.25rem', marginTop: '0.25rem' }}>
                  <span className={`class-badge class-${p.class}`}>{p.class.toUpperCase()}</span>
                  <span
                    style={{
                      fontSize: '0.6875rem',
                      fontWeight: 600,
                      padding: '0.125rem 0.375rem',
                      borderRadius: '4px',
                      background: p.enabled ? 'rgba(16, 185, 129, 0.15)' : 'rgba(239, 68, 68, 0.15)',
                      color: p.enabled ? '#10b981' : '#ef4444',
                    }}
                  >
                    {p.enabled ? 'ENABLED' : 'DISABLED'}
                  </span>
                </div>
              </div>
              <StatusBadge
                status={p.probe_status === 'healthy' ? 'ready' : 'degraded'}
                label={p.probe_status.toUpperCase()}
              />
            </div>

            {p.endpoint_url && (
              <div style={{ fontSize: '0.75rem', margin: '0.5rem 0', wordBreak: 'break-all' }}>
                <span className="text-dim">Endpoint: </span>
                <code className="font-mono">{p.endpoint_url}</code>
              </div>
            )}

            <div style={{ fontSize: '0.75rem', margin: '0.5rem 0', display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
              <span className="text-dim">SecretRef: <code>{p.secret_ref.ref_name || 'none'}</code></span>
              <span style={{ color: p.secret_ref.configured ? '#10b981' : '#f59e0b', fontWeight: 600 }}>
                {p.secret_ref.configured ? '✓ Configured' : '✕ Missing'}
              </span>
            </div>

            <div className="provider-caps-strip">
              {p.capabilities.map((c) => (
                <span key={c} className="provider-cap-pill">
                  {c}
                </span>
              ))}
            </div>

            <div className="provider-models-list">
              <span className="meta-label">Active Model Endpoints:</span>
              {p.models.map((m) => (
                <div key={m.id} className="model-row font-mono text-xs">
                  <span className="model-id">{m.id}</span>
                  <span className="model-stats text-dim">
                    {Math.round(m.context_window / 1000)}k ctx • {m.latency_p95_ms}ms p95
                  </span>
                </div>
              ))}
            </div>

            <div className="provider-actions" style={{ display: 'flex', gap: '0.5rem', marginTop: '0.75rem' }}>
              <Button
                variant="secondary"
                size="sm"
                onClick={() => handleProbeSingle(p.id)}
                disabled={probingProviderId === p.id}
              >
                {probingProviderId === p.id ? 'Probing…' : 'Probe'}
              </Button>
              <Button
                variant="secondary"
                size="sm"
                onClick={() => setSelectedSecretProvider(p)}
              >
                SecretRef
              </Button>
              <Button
                variant="secondary"
                size="sm"
                onClick={() => setSelectedConfigProvider(p)}
              >
                Config
              </Button>
            </div>

            <div className="provider-footer text-xs font-mono text-dim" style={{ marginTop: '0.5rem' }}>
              Last probe: {new Date(p.last_probed_at).toLocaleTimeString()}
            </div>
          </div>
        ))}
      </div>

      {/* Router Explanation Matrix */}
      <div className="router-matrix-section">
        <h3 className="section-title">Automated Task Routing Matrix & Rationale</h3>
        <div className="table-responsive">
          <table className="data-table" aria-label="Router Matrix Table">
            <thead>
              <tr>
                <th>Workflow Intent</th>
                <th>Selected Model</th>
                <th>Provider</th>
                <th>Selection Rationale</th>
                <th>Action</th>
              </tr>
            </thead>
            <tbody>
              {data.routing_decisions.map((d) => (
                <tr key={d.intent}>
                  <td>
                    <code className="intent-badge">{d.intent}</code>
                  </td>
                  <td>
                    <span className="font-mono text-xs font-semibold">{d.selected_model}</span>
                    {d.is_pinned && <span className="pinned-tag">PINNED</span>}
                  </td>
                  <td>
                    <span className="font-mono text-xs text-dim">{d.provider_id}</span>
                  </td>
                  <td className="rationale-cell text-xs">{d.rationale}</td>
                  <td>
                    <Button
                      variant={d.is_pinned ? 'secondary' : 'ghost'}
                      size="sm"
                      onClick={() => handleTogglePin(d)}
                    >
                      {d.is_pinned ? 'Unpin' : 'Pin Model'}
                    </Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>

      {selectedConfigProvider && (
        <ConfigureProviderModal
          provider={selectedConfigProvider}
          isOpen={!!selectedConfigProvider}
          onClose={() => setSelectedConfigProvider(null)}
          onUpdated={fetchProviders}
        />
      )}

      {selectedSecretProvider && (
        <ProviderSecretModal
          provider={selectedSecretProvider}
          isOpen={!!selectedSecretProvider}
          onClose={() => setSelectedSecretProvider(null)}
          onUpdated={fetchProviders}
        />
      )}
    </div>
  );
}
