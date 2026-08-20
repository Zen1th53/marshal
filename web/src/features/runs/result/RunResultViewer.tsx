import { useState, useEffect, useCallback } from 'react';
import { api } from '../../../api/client';
import { Button } from '../../../components/ui';
import { LoadingState, ErrorState } from '../../../components/state';
import { useToast } from '../../../components/toast';

interface RunResultViewerProps {
  runId: string;
}

interface RunResultData {
  run_id: string;
  base_commit: string;
  head_commit: string;
  files_summary: Array<{
    path: string;
    status: string;
    insertions: number;
    deletions: number;
  }>;
  artifacts: Array<{
    id: string;
    name: string;
    sha256: string;
    size_bytes: number;
    content_type: string;
  }>;
  worktree_status: string;
  checkpoint_id?: string;
  can_recover: boolean;
  created_at: string;
}

export function RunResultViewer({ runId }: RunResultViewerProps) {
  const [result, setResult] = useState<RunResultData | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [isRecovering, setIsRecovering] = useState(false);
  const { addToast } = useToast();

  const fetchResult = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const resp = await api.getRunResult(runId);
      setResult(resp);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to load run execution results');
    } finally {
      setLoading(false);
    }
  }, [runId]);

  useEffect(() => {
    void fetchResult();
  }, [fetchResult]);

  const handleRestoreCheckpoint = async () => {
    if (!result?.checkpoint_id) return;
    setIsRecovering(true);
    try {
      await api.recoverRun(runId);
      addToast({
        type: 'success',
        message: `Worktree state successfully restored from checkpoint ${result.checkpoint_id}.`,
      });
    } catch (err: unknown) {
      addToast({
        type: 'error',
        message: err instanceof Error ? err.message : 'Failed to recover from checkpoint',
      });
    } finally {
      setIsRecovering(false);
    }
  };

  if (loading) return <LoadingState message="Querying run artifacts & worktree diffs…" />;
  if (error) return <ErrorState severity="error" message={error} onRetry={fetchResult} />;
  if (!result) return null;

  return (
    <div className="run-result-viewer">
      {/* Changed Files Summary */}
      <div className="result-section">
        <h4 className="section-subtitle">Modified Codebase Files ({result.files_summary.length})</h4>
        <div className="table-responsive">
          <table className="data-table" aria-label="Modified Files Table">
            <thead>
              <tr>
                <th>File Path</th>
                <th>Status</th>
                <th>Diff Stats</th>
              </tr>
            </thead>
            <tbody>
              {result.files_summary.map((f) => (
                <tr key={f.path}>
                  <td>
                    <code className="font-mono text-xs">{f.path}</code>
                  </td>
                  <td>
                    <span className={`file-status-badge status-${f.status}`}>{f.status.toUpperCase()}</span>
                  </td>
                  <td>
                    <span className="diff-stat-ins">+{f.insertions}</span>{' '}
                    <span className="diff-stat-del">-{f.deletions}</span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>

      {/* Generated Artifacts & Digests */}
      <div className="result-section">
        <h4 className="section-subtitle">Generated Evidence Artifacts ({result.artifacts.length})</h4>
        <div className="table-responsive">
          <table className="data-table" aria-label="Evidence Artifacts Table">
            <thead>
              <tr>
                <th>Artifact Name</th>
                <th>SHA-256 Digest</th>
                <th>Size</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {result.artifacts.map((a) => (
                <tr key={a.id}>
                  <td>
                    <span className="font-medium text-xs">{a.name}</span>
                  </td>
                  <td>
                    <code className="artifact-sha font-mono text-xs">{a.sha256.slice(0, 16)}…</code>
                  </td>
                  <td>
                    <span className="text-dim text-xs font-mono">{a.size_bytes} B</span>
                  </td>
                  <td>
                    <a
                      href={`/api/v1/artifacts/${a.id}/download`}
                      className="btn btn-secondary btn-sm"
                      download={a.name}
                      aria-label={`Download artifact ${a.name}`}
                    >
                      Download
                    </a>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>

      {/* Checkpoint Recovery */}
      {result.can_recover && result.checkpoint_id && (
        <div className="recovery-card">
          <div className="recovery-info">
            <span className="recovery-title">Checkpoint Recovery Point Available</span>
            <span className="recovery-desc font-mono text-xs">ID: {result.checkpoint_id} (Retention: {result.worktree_status})</span>
          </div>
          <Button
            variant="secondary"
            size="sm"
            onClick={handleRestoreCheckpoint}
            disabled={isRecovering}
          >
            {isRecovering ? 'Restoring…' : 'Restore Checkpoint'}
          </Button>
        </div>
      )}
    </div>
  );
}
