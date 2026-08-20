import { useState, useEffect, useCallback } from 'react';
import { api } from '../api/client';
import { useRealtimeEvent } from '../realtime/useRealtime';
import { StatusBadge, Button } from '../components/ui';
import { LoadingState, ErrorState, EmptyState } from '../components/state';

interface ReviewQueueItem {
  task_id: string;
  title: string;
  stage: string;
  risk: string;
  owner: string;
  base_commit: string;
  head_commit: string;
  is_stale_head: boolean;
  approvals_count: number;
  required_quorum: number;
  blocker_count: number;
  submitted_at: string;
}

export function Review() {
  const [items, setItems] = useState<ReviewQueueItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [stageFilter, setStageFilter] = useState('all');
  const [riskFilter, setRiskFilter] = useState('all');

  const fetchQueue = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const resp = await api.getReviewQueue({
        stage: stageFilter !== 'all' ? stageFilter : undefined,
        risk: riskFilter !== 'all' ? riskFilter : undefined,
      });
      setItems(resp.items);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to load review center queue');
    } finally {
      setLoading(false);
    }
  }, [stageFilter, riskFilter]);

  useEffect(() => {
    void fetchQueue();
  }, [fetchQueue]);

  // Realtime updates on status changes
  useRealtimeEvent('task.status', () => {
    void fetchQueue();
  });

  const getStageBadge = (stage: string) => {
    switch (stage) {
      case 'plan_review':
        return <span className="stage-badge stage-plan">PLAN REVIEW</span>;
      case 'gate_review':
        return <span className="stage-badge stage-gate">GATE REVIEW</span>;
      case 'merge_approval':
        return <span className="stage-badge stage-merge">MERGE APPROVAL</span>;
      default:
        return <span className="stage-badge">{stage.toUpperCase()}</span>;
    }
  };

  return (
    <div className="review-queue-container">
      <div className="review-header">
        <div className="review-headline">
          <h2 className="review-title">Evidence & Quorum Review Center</h2>
          <span className="review-count">{items.length} Pending Review Items</span>
        </div>
        <Button variant="secondary" size="sm" onClick={fetchQueue}>
          Refresh Queue
        </Button>
      </div>

      {/* Filter Toolbar */}
      <div className="filter-toolbar">
        <div className="filter-group">
          <select
            className="filter-select"
            value={stageFilter}
            onChange={(e) => setStageFilter(e.target.value)}
            aria-label="Filter by review stage"
          >
            <option value="all">All Stages</option>
            <option value="plan_review">Plan Review</option>
            <option value="gate_review">Gate Review</option>
            <option value="merge_approval">Merge Approval</option>
          </select>
        </div>

        <div className="filter-group">
          <select
            className="filter-select"
            value={riskFilter}
            onChange={(e) => setRiskFilter(e.target.value)}
            aria-label="Filter by risk level"
          >
            <option value="all">All Risk Levels</option>
            <option value="LOW">Low Risk</option>
            <option value="MEDIUM">Medium Risk</option>
            <option value="HIGH">High Risk</option>
            <option value="CRITICAL">Critical Risk</option>
          </select>
        </div>
      </div>

      {/* Table Content */}
      {loading ? (
        <LoadingState message="Loading review queue & verification attestations…" />
      ) : error ? (
        <ErrorState severity="error" message={error} onRetry={fetchQueue} />
      ) : items.length === 0 ? (
        <EmptyState
          title="No pending review items"
          description="All tasks have met their required verification quorum or no active reviews match your filter."
        />
      ) : (
        <div className="table-responsive">
          <table className="data-table" aria-label="Review Queue Table">
            <thead>
              <tr>
                <th>Task ID</th>
                <th>Objective</th>
                <th>Stage</th>
                <th>Risk</th>
                <th>Quorum Status</th>
                <th>Commit Range</th>
                <th>Blockers</th>
              </tr>
            </thead>
            <tbody>
              {items.map((item) => (
                <tr key={item.task_id}>
                  <td>
                    <code className="task-id-code">{item.task_id}</code>
                    {item.is_stale_head && (
                      <span className="stale-pill" title="Commits advanced after initial submission">
                        STALE
                      </span>
                    )}
                  </td>
                  <td className="task-title-cell">{item.title}</td>
                  <td>{getStageBadge(item.stage)}</td>
                  <td>
                    <span className={`risk-badge risk-${item.risk.toLowerCase()}`}>{item.risk}</span>
                  </td>
                  <td>
                    <span className="font-mono text-xs">
                      {item.approvals_count} / {item.required_quorum} Signed
                    </span>
                  </td>
                  <td>
                    <span className="commit-range font-mono text-xs">
                      {item.base_commit.slice(0, 7)} → {item.head_commit.slice(0, 7)}
                    </span>
                  </td>
                  <td>
                    {item.blocker_count > 0 ? (
                      <StatusBadge status="degraded" label={`${item.blocker_count} BLOCKER`} />
                    ) : (
                      <StatusBadge status="ready" label="CLEAR" />
                    )}
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
