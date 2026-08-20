import { useState, useEffect, useCallback } from 'react';
import { api } from '../api/client';
import { StatusBadge, Button } from '../components/ui';
import { CorrelationLink } from '../components/audit/CorrelationLink';
import { LoadingState, ErrorState, EmptyState } from '../components/state';

interface AuditActor {
  principal_id: string;
  role: string;
}

interface AuditEvent {
  id: string;
  actor: AuditActor;
  action: string;
  resource_type: string;
  resource_id: string;
  outcome: string;
  correlation_id: string;
  timestamp: string;
  details: Record<string, any>;
}

export function Audit() {
  const [events, setEvents] = useState<AuditEvent[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [outcomeFilter, setOutcomeFilter] = useState('all');
  const [actionFilter, setActionFilter] = useState('all');

  const fetchAuditEvents = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const resp = await api.listAuditEvents({
        outcome: outcomeFilter !== 'all' ? outcomeFilter : undefined,
        action: actionFilter !== 'all' ? actionFilter : undefined,
      });
      setEvents(resp.events);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to query global audit timeline');
    } finally {
      setLoading(false);
    }
  }, [outcomeFilter, actionFilter]);

  useEffect(() => {
    void fetchAuditEvents();
  }, [fetchAuditEvents]);

  return (
    <div className="audit-container">
      <div className="audit-header">
        <div className="audit-headline">
          <h2 className="audit-title">Global Governance & Audit Timeline</h2>
          <span className="audit-subtitle">
            Immutable causal log of all administrative, mutation and security decisions
          </span>
        </div>
        <div className="audit-actions-group">
          <a
            href="/api/v1/audit/export"
            className="btn btn-secondary btn-sm"
            download="marshal_audit_export.json"
          >
            Export Log (.json)
          </a>
          <Button variant="ghost" size="sm" onClick={fetchAuditEvents}>
            Refresh
          </Button>
        </div>
      </div>

      {/* Filter Toolbar */}
      <div className="filter-toolbar">
        <div className="filter-group">
          <select
            className="filter-select"
            value={outcomeFilter}
            onChange={(e) => setOutcomeFilter(e.target.value)}
            aria-label="Filter by audit outcome"
          >
            <option value="all">All Outcomes</option>
            <option value="success">Success</option>
            <option value="denied">Denied (Forbidden)</option>
            <option value="failed">Failed / Error</option>
          </select>
        </div>

        <div className="filter-group">
          <select
            className="filter-select"
            value={actionFilter}
            onChange={(e) => setActionFilter(e.target.value)}
            aria-label="Filter by action type"
          >
            <option value="all">All Actions</option>
            <option value="task.merge">Task Merge</option>
            <option value="quorum.attest">Quorum Attestation</option>
            <option value="task.delete">Task Delete</option>
            <option value="router.override">Router Override</option>
          </select>
        </div>
      </div>

      {/* Table Content */}
      {loading ? (
        <LoadingState message="Querying immutable audit log & verifying correlation trace integrity…" />
      ) : error ? (
        <ErrorState severity="error" message={error} onRetry={fetchAuditEvents} />
      ) : events.length === 0 ? (
        <EmptyState
          title="No audit events found"
          description="No governance or administrative audit records match your current filter parameters."
        />
      ) : (
        <div className="table-responsive">
          <table className="data-table" aria-label="Audit Timeline Table">
            <thead>
              <tr>
                <th>Event ID</th>
                <th>Actor Principal</th>
                <th>Action</th>
                <th>Target Resource</th>
                <th>Outcome</th>
                <th>Trace Correlation</th>
                <th>Timestamp</th>
              </tr>
            </thead>
            <tbody>
              {events.map((ev) => (
                <tr key={ev.id}>
                  <td>
                    <code className="audit-id-code font-mono text-xs">{ev.id}</code>
                  </td>
                  <td>
                    <div className="actor-cell font-mono text-xs">
                      <span className="actor-id">{ev.actor.principal_id}</span>
                      <span className="text-dim">({ev.actor.role})</span>
                    </div>
                  </td>
                  <td>
                    <span className="action-tag font-mono text-xs">{ev.action}</span>
                  </td>
                  <td>
                    <div className="resource-cell font-mono text-xs">
                      <span className="text-dim">{ev.resource_type}:</span>
                      <code>{ev.resource_id}</code>
                    </div>
                  </td>
                  <td>
                    <StatusBadge
                      status={ev.outcome === 'success' ? 'ready' : 'degraded'}
                      label={ev.outcome.toUpperCase()}
                    />
                  </td>
                  <td>
                    <CorrelationLink correlationId={ev.correlation_id} />
                  </td>
                  <td>
                    <span className="font-mono text-xs text-dim">
                      {new Date(ev.timestamp).toLocaleTimeString()}
                    </span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
