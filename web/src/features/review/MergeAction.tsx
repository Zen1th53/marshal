import { useState, useEffect, useCallback } from 'react';
import { api } from '../../api/client';
import { StatusBadge, Button } from '../../components/ui';
import { AuthorizedAction } from '../../components/security/AuthorizedAction';
import { CorrelationLink } from '../../components/audit/CorrelationLink';
import { LoadingState, ErrorState } from '../../components/state';
import { useToast } from '../../components/toast';

interface MergeActionProps {
  taskId: string;
  onMerged?: () => void;
}

interface PreflightData {
  task_id: string;
  is_eligible: boolean;
  expected_head: string;
  target_branch: string;
  quorum_met: boolean;
  has_veto: boolean;
  is_stale_head: boolean;
  gating_checks: string[];
  denial_reason?: string;
}

interface MergeResult {
  task_id: string;
  merged: boolean;
  merge_commit: string;
  target_branch: string;
  merged_at: string;
  correlation_id: string;
}

export function MergeAction({ taskId, onMerged }: MergeActionProps) {
  const [preflight, setPreflight] = useState<PreflightData | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [mergeResult, setMergeResult] = useState<MergeResult | null>(null);
  const { addToast } = useToast();

  const fetchPreflight = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const resp = await api.getMergePreflight(taskId);
      setPreflight(resp);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to check merge preflight eligibility');
    } finally {
      setLoading(false);
    }
  }, [taskId]);

  useEffect(() => {
    void fetchPreflight();
  }, [fetchPreflight]);

  const handleMerge = async () => {
    if (!preflight) return;
    try {
      const result = await api.executeMerge(taskId, {
        expected_head: preflight.expected_head,
        strategy: 'squash',
      });
      setMergeResult(result);
      addToast({
        type: 'success',
        message: `Task successfully merged into ${result.target_branch} (${result.merge_commit.slice(0, 7)})`,
      });
      onMerged?.();
    } catch (err: unknown) {
      addToast({
        type: 'error',
        message: err instanceof Error ? err.message : 'Failed to finalize merge',
      });
    }
  };

  if (loading) return <LoadingState message="Validating merge gates & quorum eligibility…" />;
  if (error) return <ErrorState severity="error" message={error} onRetry={fetchPreflight} />;
  if (!preflight) return null;

  return (
    <div className="merge-action-container" role="region" aria-label="Merge Finalization Workspace">
      {mergeResult ? (
        <div className="merge-success-card">
          <div className="merge-success-header">
            <StatusBadge status="ready" label="MERGED" />
            <h4 className="merge-success-title">Task Codebase Merged Successfully</h4>
          </div>
          <div className="merge-success-details font-mono text-xs">
            <span>Commit: <code>{mergeResult.merge_commit}</code></span>
            <span>Target: <code>{mergeResult.target_branch}</code></span>
            <span>Trace: <CorrelationLink correlationId={mergeResult.correlation_id} /></span>
          </div>
        </div>
      ) : (
        <div className="merge-preflight-card">
          <div className="preflight-header">
            <div className="preflight-title-group">
              <h4 className="section-subtitle">Merge Finalization Preflight</h4>
              <StatusBadge
                status={preflight.is_eligible ? 'ready' : 'degraded'}
                label={preflight.is_eligible ? 'ELIGIBLE TO MERGE' : 'MERGE BLOCKED'}
              />
            </div>
            <span className="target-branch-badge font-mono text-xs">Target: {preflight.target_branch}</span>
          </div>

          {/* Denial Notice if any */}
          {preflight.denial_reason && (
            <div className="alert-banner alert-warning" role="alert">
              ⚠️ {preflight.denial_reason}
            </div>
          )}

          {/* Gating Verification Checks Checklist */}
          <div className="gating-checks-list">
            <span className="meta-label">Automated Gate Assertions:</span>
            {preflight.gating_checks.map((chk, idx) => (
              <div key={idx} className="gate-check-item font-mono text-xs">
                <span className="check-mark">✓</span> {chk}
              </div>
            ))}
          </div>

          <div className="merge-actions-row">
            <AuthorizedAction
              authority="task:merge"
              onAction={handleMerge}
              disabled={!preflight.is_eligible}
              variant="primary"
              size="sm"
              isDestructive={true}
              confirmTitle="Confirm Codebase Merge"
              confirmMessage={`Are you sure you want to merge task ${taskId} into ${preflight.target_branch}? This action will finalize all reviews.`}
            >
              🚀 Finalize & Merge Task
            </AuthorizedAction>
            <Button variant="ghost" size="sm" onClick={fetchPreflight}>
              Revalidate Preflight
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}
