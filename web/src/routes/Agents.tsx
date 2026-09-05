import { useState, useEffect, useCallback, useMemo } from 'react';
import { api } from '../api/client';
import { useRealtimeEvent } from '../realtime/useRealtime';
import { StatusBadge, Button } from '../components/ui';
import { LoadingState, ErrorState, EmptyState } from '../components/state';
import { AgentDetail } from './AgentDetail';
import { CreateAgentModal } from '../features/agents/forms/CreateAgentModal';
import type { AgentSummaryDTO } from '../api/types';

interface AgentsProps {
  onNavigateTask?: (taskId: string) => void;
}

export function Agents({ onNavigateTask }: AgentsProps) {
  const [agents, setAgents] = useState<AgentSummaryDTO[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [search, setSearch] = useState('');
  const [providerFilter, setProviderFilter] = useState('all');
  const [selectedAgentId, setSelectedAgentId] = useState<string | null>(null);
  const [isCreateOpen, setIsCreateOpen] = useState(false);

  const fetchAgents = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const resp = await api.getAgents();
      setAgents(resp.items ?? []);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to fetch agent fleet');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void fetchAgents();
  }, [fetchAgents]);

  useRealtimeEvent('agent.status', () => {
    void fetchAgents();
  });

  const availableProviders = useMemo(() => {
    const provs = new Set<string>();
    agents.forEach((a) => {
      if (a.provider) {
        provs.add(a.provider.toLowerCase());
      }
    });
    // Ensure standard providers are present for clean UX
    ['claude', 'codex', 'gemini', 'opencode'].forEach((p) => provs.add(p));
    return ['all', ...Array.from(provs)];
  }, [agents]);

  const filteredAgents = agents.filter((a) => {
    const matchesSearch =
      a.name.toLowerCase().includes(search.toLowerCase()) ||
      a.id.toLowerCase().includes(search.toLowerCase());
    const matchesProvider =
      providerFilter === 'all' ||
      (a.provider && a.provider.toLowerCase() === providerFilter.toLowerCase());
    return matchesSearch && matchesProvider;
  });

  return (
    <div className="agents-container">
      <div className="agents-header">
        <div className="agents-headline">
          <h2 className="agents-title">Autonomous Agent Fleet</h2>
          <span className="agents-count">{agents.length} Registered Agents</span>
        </div>
        <div style={{ display: 'flex', gap: '0.5rem' }}>
          <Button variant="primary" size="sm" onClick={() => setIsCreateOpen(true)}>
            + Register Agent
          </Button>
          <Button variant="secondary" size="sm" onClick={fetchAgents}>
            Refresh Fleet
          </Button>
        </div>
      </div>

      {/* Filter Bar */}
      <div className="filter-bar">
        <div className="search-input-wrapper">
          <span className="search-icon" aria-hidden="true">🔍</span>
          <input
            type="text"
            className="filter-input"
            placeholder="Filter agents by name or ID…"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
        </div>

        <div className="filter-tabs" role="tablist" aria-label="Filter by provider">
          {availableProviders.map((prov) => (
            <button
              key={prov}
              type="button"
              role="tab"
              aria-selected={providerFilter === prov}
              className={`filter-tab ${providerFilter === prov ? 'active' : ''}`}
              onClick={() => setProviderFilter(prov)}
            >
              {prov.toUpperCase()}
            </button>
          ))}
        </div>
      </div>

      {/* Fleet Table / Grid */}
      {loading && agents.length === 0 ? (
        <LoadingState message="Querying agent roster…" />
      ) : error ? (
        <ErrorState severity="error" message={error} onRetry={fetchAgents} />
      ) : filteredAgents.length === 0 ? (
        <EmptyState
          title="No Agents Found"
          description={search ? 'No agents match the specified filter query.' : 'No agents currently registered in fleet.'}
        />
      ) : (
        <div className="agent-grid">
          {filteredAgents.map((agent) => (
            <div
              key={agent.id}
              className="agent-card"
              onClick={() => setSelectedAgentId(agent.id)}
              role="button"
              tabIndex={0}
              onKeyDown={(e) => {
                if (e.key === 'Enter' || e.key === ' ') {
                  e.preventDefault();
                  setSelectedAgentId(agent.id);
                }
              }}
            >
              <div className="agent-card-header">
                <div className="agent-card-title-group">
                  <h3 className="agent-card-name">{agent.name}</h3>
                  <code className="agent-card-id">{agent.id}</code>
                </div>
                <StatusBadge
                  status={agent.status === 'disabled' ? 'degraded' : 'ready'}
                  label={agent.status.toUpperCase()}
                />
              </div>

              <div className="agent-card-body">
                <div className="agent-card-meta">
                  <span className="meta-item">
                    Role: <strong>{(agent.role || 'developer').toUpperCase()}</strong>
                  </span>
                  <span className="meta-item">
                    Provider: <strong>{agent.provider?.toUpperCase() ?? 'GENERIC'}</strong>
                  </span>
                  <span className="meta-item">
                    Model: <code>{agent.model ?? 'default'}</code>
                  </span>
                  <span className="meta-item">
                    Completed: <strong>{agent.completed_task_count ?? 0}</strong>
                  </span>
                </div>

                {agent.capabilities && agent.capabilities.length > 0 && (
                  <div className="agent-card-caps">
                    {agent.capabilities.slice(0, 3).map((cap) => (
                      <span key={cap} className="cap-pill">
                        {cap}
                      </span>
                    ))}
                    {agent.capabilities.length > 3 && (
                      <span className="cap-pill-more">+{agent.capabilities.length - 3}</span>
                    )}
                  </div>
                )}
              </div>
            </div>
          ))}
        </div>
      )}

      {selectedAgentId && (
        <AgentDetail
          agentId={selectedAgentId}
          onClose={() => setSelectedAgentId(null)}
          onNavigateTask={onNavigateTask}
          onAgentMutated={fetchAgents}
        />
      )}

      {isCreateOpen && (
        <CreateAgentModal
          isOpen={isCreateOpen}
          onClose={() => setIsCreateOpen(false)}
          onCreated={() => {
            void fetchAgents();
          }}
        />
      )}
    </div>
  );
}
