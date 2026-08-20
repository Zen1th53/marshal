import { useState, useEffect, useCallback, type FormEvent } from 'react';
import { api } from '../api/client';
import { StatusBadge, Button } from '../components/ui';
import { LoadingState, ErrorState, EmptyState } from '../components/state';
import { MemoryDetail } from './MemoryDetail';
import { RetrievalExplainability } from '../features/memory/retrieval/RetrievalExplainability';

interface MemoryItem {
  id: string;
  project_id: string;
  scope: string;
  scope_id: string;
  kind: string;
  title: string;
  body: string;
  lifecycle: string;
  authority: string;
  confidence: number;
  observed_at: string;
  retrieval_score: number;
  retrieval_reason: string;
}

export function Memory() {
  const [items, setItems] = useState<MemoryItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [indexStatus, setIndexStatus] = useState('healthy');

  const [queryInput, setQueryInput] = useState('');
  const [activeQuery, setActiveQuery] = useState('');
  const [scopeFilter, setScopeFilter] = useState('all');
  const [kindFilter, setKindFilter] = useState('all');
  const [lifecycleFilter, setLifecycleFilter] = useState('all');

  const [selectedRecord, setSelectedRecord] = useState<MemoryItem | null>(null);
  const [showExplain, setShowExplain] = useState(false);

  const fetchMemory = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const resp = await api.searchMemory({
        query: activeQuery || undefined,
        scope: scopeFilter !== 'all' ? scopeFilter : undefined,
        kind: kindFilter !== 'all' ? kindFilter : undefined,
        lifecycle: lifecycleFilter !== 'all' ? lifecycleFilter : undefined,
      });
      setItems(resp.items);
      setIndexStatus(resp.index_status);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to search memory corpus');
    } finally {
      setLoading(false);
    }
  }, [activeQuery, scopeFilter, kindFilter, lifecycleFilter]);

  useEffect(() => {
    void fetchMemory();
  }, [fetchMemory]);

  const handleSearchSubmit = (e: FormEvent) => {
    e.preventDefault();
    setActiveQuery(queryInput);
  };

  return (
    <div className="memory-container">
      <div className="memory-header">
        <div className="memory-headline">
          <h2 className="memory-title">Memory Explorer & Hybrid Retrieval</h2>
          <span className="memory-subtitle">
            Episodic, architectural and procedural memory corpus introspection
          </span>
        </div>
        <div className="flex-row items-center gap-2">
          <Button variant="secondary" size="sm" onClick={() => setShowExplain(true)}>
            Explain Ranking (RRF)
          </Button>
          <div className="index-status-pill">
            <span className="text-dim text-xs">Index:</span>
            <StatusBadge
              status={indexStatus === 'healthy' ? 'ready' : 'degraded'}
              label={indexStatus.toUpperCase()}
            />
          </div>
        </div>
      </div>

      {/* Hybrid Search & Filter Toolbar */}
      <form onSubmit={handleSearchSubmit} className="memory-search-toolbar">
        <div className="search-input-group">
          <input
            type="search"
            className="input font-mono text-sm"
            placeholder="Search memory title, body or ID…"
            value={queryInput}
            onChange={(e) => setQueryInput(e.target.value)}
            aria-label="Search memory query"
          />
          <Button type="submit" variant="primary" size="sm">
            Search
          </Button>
          {activeQuery && (
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => {
                setQueryInput('');
                setActiveQuery('');
              }}
            >
              Clear
            </Button>
          )}
        </div>

        <div className="filter-group">
          <select
            className="filter-select"
            value={scopeFilter}
            onChange={(e) => setScopeFilter(e.target.value)}
            aria-label="Filter by memory scope"
          >
            <option value="all">All Scopes</option>
            <option value="global">Global</option>
            <option value="project">Project</option>
            <option value="task">Task</option>
            <option value="session">Session</option>
          </select>
        </div>

        <div className="filter-group">
          <select
            className="filter-select"
            value={kindFilter}
            onChange={(e) => setKindFilter(e.target.value)}
            aria-label="Filter by memory kind"
          >
            <option value="all">All Kinds</option>
            <option value="decision">Decision</option>
            <option value="procedure">Procedure</option>
            <option value="episode">Episode</option>
            <option value="belief">Belief</option>
          </select>
        </div>

        <div className="filter-group">
          <select
            className="filter-select"
            value={lifecycleFilter}
            onChange={(e) => setLifecycleFilter(e.target.value)}
            aria-label="Filter by lifecycle"
          >
            <option value="all">All Lifecycles</option>
            <option value="active">Active</option>
            <option value="candidate">Candidate</option>
            <option value="consolidated">Consolidated</option>
            <option value="evicted">Evicted</option>
          </select>
        </div>
      </form>

      {/* Memory Results */}
      {loading ? (
        <LoadingState message="Executing hybrid vector + lexical memory retrieval…" />
      ) : error ? (
        <ErrorState severity="error" message={error} onRetry={fetchMemory} />
      ) : items.length === 0 ? (
        <EmptyState
          title="No memory items found"
          description="Try broadening your search term or selecting a different scope/kind filter."
        />
      ) : (
        <div className="memory-grid">
          {items.map((m) => (
            <div
              key={m.id}
              className="memory-card clickable"
              onClick={() => setSelectedRecord(m)}
              role="button"
              tabIndex={0}
              onKeyDown={(e) => e.key === 'Enter' && setSelectedRecord(m)}
            >
              <div className="memory-card-header">
                <div className="memory-badges-row">
                  <span className={`kind-badge kind-${m.kind}`}>{m.kind.toUpperCase()}</span>
                  <span className="scope-badge font-mono text-xs">{m.scope}</span>
                </div>
                <div className="score-pill font-mono text-xs">
                  ★ {(m.retrieval_score * 100).toFixed(0)}%
                </div>
              </div>

              <h4 className="memory-card-title">{m.title}</h4>
              <p className="memory-card-body text-xs">{m.body}</p>

              <div className="memory-card-footer">
                <code className="memory-id-tag font-mono">{m.id}</code>
                <StatusBadge
                  status={m.authority === 'verified' ? 'ready' : 'degraded'}
                  label={m.authority.toUpperCase()}
                />
              </div>
            </div>
          ))}
        </div>
      )}

      {/* Record Inspector Modal */}
      {selectedRecord && (
        <MemoryDetail
          memoryId={selectedRecord.id}
          onClose={() => setSelectedRecord(null)}
        />
      )}

      {/* RRF Explainability Modal */}
      {showExplain && (
        <RetrievalExplainability
          query={activeQuery || 'architectural loopback invariant'}
          onClose={() => setShowExplain(false)}
        />
      )}
    </div>
  );
}
