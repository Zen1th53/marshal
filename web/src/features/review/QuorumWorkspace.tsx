import { useState, useEffect, useCallback } from 'react';
import { api } from '../../api/client';
import { StatusBadge, Button } from '../../components/ui';
import { LoadingState, ErrorState } from '../../components/state';
import { useToast } from '../../components/toast';

interface QuorumWorkspaceProps {
  taskId: string;
  onDecisionSubmitted?: () => void;
}

interface Attestation {
  reviewer_id: string;
  provider: string;
  role: string;
  decision: string;
  comment: string;
  commit_hash: string;
  signed_at: string;
}

interface QuorumData {
  task_id: string;
  head_commit: string;
  required_quorum: number;
  current_approvals_count: number;
  has_veto: boolean;
  veto_reason?: string;
  is_quorum_met: boolean;
  independence_note: string;
  attestations: Attestation[];
}

export function QuorumWorkspace({ taskId, onDecisionSubmitted }: QuorumWorkspaceProps) {
  const [data, setData] = useState<QuorumData | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [decision, setDecision] = useState<'approved' | 'rejected' | 'vetoed'>('approved');
  const [comment, setComment] = useState('');
  const [isSubmitting, setIsSubmitting] = useState(false);
  const { addToast } = useToast();

  const fetchQuorum = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const resp = await api.getTaskQuorum(taskId);
      setData(resp);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to fetch quorum status');
    } finally {
      setLoading(false);
    }
  }, [taskId]);

  useEffect(() => {
    void fetchQuorum();
  }, [fetchQuorum]);

  const handleSubmitDecision = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!data) return;

    setIsSubmitting(true);
    try {
      await api.submitQuorumDecision(taskId, {
        decision,
        comment: comment.trim() ? comment.trim() : 'Attestation verified by operator/auditor',
        commit_hash: data.head_commit,
      });

      addToast({
        type: decision === 'vetoed' ? 'error' : 'success',
        message: `Decision "${decision.toUpperCase()}" recorded for ${taskId}.`,
      });

      setComment('');
      await fetchQuorum();
      onDecisionSubmitted?.();
    } catch (err: unknown) {
      addToast({
        type: 'error',
        message: err instanceof Error ? err.message : 'Failed to record decision',
      });
    } finally {
      setIsSubmitting(false);
    }
  };

  if (loading) return <LoadingState message="Checking multi-agent quorum attestations…" />;
  if (error) return <ErrorState severity="error" message={error} onRetry={fetchQuorum} />;
  if (!data) return null;

  return (
    <div className="quorum-workspace-container" role="region" aria-label="Quorum Attestation Workspace">
      {/* Quorum Progress Header */}
      <div className="quorum-header-card">
        <div className="quorum-progress-info">
          <span className="quorum-progress-count font-mono">
            {data.current_approvals_count} / {data.required_quorum} Independent Signatures
          </span>
          <StatusBadge
            status={data.is_quorum_met ? 'ready' : 'degraded'}
            label={data.has_veto ? 'VETOED' : data.is_quorum_met ? 'QUORUM MET' : 'PENDING QUORUM'}
          />
        </div>
        <div className="quorum-independence-note">
          ℹ️ {data.independence_note}
        </div>
      </div>

      {/* Existing Attestation Signatures */}
      <div className="attestations-list-section">
        <h4 className="section-subtitle">Recorded Signatures ({data.attestations.length})</h4>
        <div className="attestations-grid">
          {data.attestations.map((a, idx) => (
            <div key={idx} className={`attestation-card decision-${a.decision}`}>
              <div className="attestation-header">
                <span className="reviewer-name font-mono">{a.reviewer_id}</span>
                <span className={`decision-pill pill-${a.decision}`}>{a.decision.toUpperCase()}</span>
              </div>
              <div className="attestation-meta font-mono text-xs">
                <span>Provider: {a.provider}</span> • <span>Role: {a.role}</span>
              </div>
              <p className="attestation-comment">{a.comment}</p>
              <div className="attestation-footer font-mono text-xs text-dim">
                <span>Commit: {a.commit_hash.slice(0, 7)}</span>
                <span>{new Date(a.signed_at).toLocaleTimeString()}</span>
              </div>
            </div>
          ))}
        </div>
      </div>

      {/* Submit Decision Form */}
      <form onSubmit={handleSubmitDecision} className="quorum-decision-form">
        <h4 className="section-subtitle">Attest & Sign Task Quorum</h4>
        <div className="form-row-two">
          <div className="form-group">
            <label htmlFor="decision-select" className="form-label">
              Decision
            </label>
            <select
              id="decision-select"
              className="form-select"
              value={decision}
              onChange={(e) => setDecision(e.target.value as any)}
            >
              <option value="approved">Approve & Sign Quorum</option>
              <option value="rejected">Reject Implementation</option>
              <option value="vetoed">Veto Task Gate (Security Blocker)</option>
            </select>
          </div>

          <div className="form-group">
            <label className="form-label">Bound Git Head</label>
            <input
              type="text"
              className="form-input font-mono"
              value={data.head_commit}
              readOnly
              disabled
            />
          </div>
        </div>

        <div className="form-group">
          <label htmlFor="decision-comment" className="form-label">
            Attestation Evidence & Rationale
          </label>
          <textarea
            id="decision-comment"
            className="form-textarea"
            rows={2}
            placeholder="Explain verification steps, adversarial check results, or reason for veto…"
            value={comment}
            onChange={(e) => setComment(e.target.value)}
          />
        </div>

        <Button
          variant={decision === 'vetoed' ? 'danger' : 'primary'}
          size="sm"
          type="submit"
          disabled={isSubmitting}
        >
          {isSubmitting ? 'Signing…' : `Submit ${decision.toUpperCase()}`}
        </Button>
      </form>
    </div>
  );
}
