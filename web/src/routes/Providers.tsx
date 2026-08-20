import { useState, useEffect, useCallback } from 'react';
import { api } from '../api/client';
import { StatusBadge, Button } from '../components/ui';
import { LoadingState, ErrorState } from '../components/state';
import { useToast } from '../components/toast';

interface ActiveModel {
  id: string;
  context_window: number;
  latency_p95_ms: number;
}

interface Provider {
  id: string;
  name: string;
  class: string;
  probe_status: string;
  capabilities: string[];
  models: ActiveModel[];
  last_probed_at: string;
}

interface RoutingDecision {
  intent: string;
  selected_model: string;
  provider_id: string;
  rationale: string;
  is_pinned: boolean;
}

interface ProviderData {
  providers: Provider[];
  routing_decisions: RoutingDecision[];
  last_evaluated_at: string;
}

export function Providers() {
  const [data, setData] = useState<ProviderData | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
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

  const handleTogglePin = async (decision: RoutingDecision) => {
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

  if (loading) return <LoadingState message="Probing provider endpoints & evaluating model routing…" />;
  if (error) return <ErrorState severity="error" message={error} onRetry={fetchProviders} />;
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
          Probe Providers
        </Button>
      </div>

      {/* Provider Fleet Cards */}
      <div className="providers-grid">
        {data.providers.map((p) => (
          <div key={p.id} className="provider-card">
            <div className="provider-card-header">
              <div className="provider-name-group">
                <h3 className="provider-name">{p.name}</h3>
                <span className={`class-badge class-${p.class}`}>{p.class.toUpperCase()}</span>
              </div>
              <StatusBadge
                status={p.probe_status === 'healthy' ? 'ready' : 'degraded'}
                label={p.probe_status.toUpperCase()}
              />
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

            <div className="provider-footer text-xs font-mono text-dim">
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
    </div>
  );
}
