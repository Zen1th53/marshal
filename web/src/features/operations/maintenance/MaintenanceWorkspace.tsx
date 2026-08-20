import { useState, useEffect, useCallback } from 'react';
import { api } from '../../../api/client';
import { StatusBadge, Button } from '../../../components/ui';
import { useToast } from '../../../components/toast';
import { LoadingState, ErrorState, EmptyState } from '../../../components/state';

interface MaintenanceJob {
  job_id: string;
  job_type: string;
  status: string;
  is_dry_run: boolean;
  target_scope: string;
  reclaimed_bytes: number;
  records_affected: number;
  audit_id: string;
  started_at: string;
  completed_at?: string;
}

export function MaintenanceWorkspace() {
  const [jobs, setJobs] = useState<MaintenanceJob[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [selectedType, setSelectedType] = useState('worktree_gc');
  const [isDryRun, setIsDryRun] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  const { addToast } = useToast();

  const fetchJobs = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const resp = await api.listMaintenanceJobs();
      setJobs(resp.jobs);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to query maintenance jobs');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void fetchJobs();
  }, [fetchJobs]);

  const handleTriggerJob = async () => {
    setSubmitting(true);
    try {
      const resp = await api.createMaintenanceJob({
        job_type: selectedType,
        is_dry_run: isDryRun,
        target_scope: selectedType === 'worktree_gc' ? 'ephemeral_worktrees' : 'vector_sqlitevec',
      });
      addToast({
        type: isDryRun ? 'info' : 'success',
        message: `${isDryRun ? 'Dry-Run completed' : 'Job executed'}: ${resp.job_id} (Audit: ${resp.audit_id})`,
      });
      await fetchJobs();
    } catch (err: unknown) {
      addToast({
        type: 'error',
        message: err instanceof Error ? err.message : 'Failed to submit maintenance job',
      });
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="maintenance-workspace-container" style={{ marginTop: 'var(--space-5)' }}>
      <div className="flex-row items-center justify-between" style={{ marginBottom: 'var(--space-3)' }}>
        <div>
          <h3 className="text-base font-semibold">GC, Retention & Index Rebuild Workspace</h3>
          <span className="text-xs text-dim">
            Dry-run and execute automated housekeeping for ephemeral worktrees, dead artifacts and vector indexes
          </span>
        </div>
      </div>

      {/* Action Controls Card */}
      <div className="maintenance-action-card" style={{ marginBottom: 'var(--space-4)' }}>
        <div className="flex-row items-center gap-3">
          <div className="form-group flex-1">
            <label className="form-label text-xs">Operation Type</label>
            <select
              className="form-input form-select text-xs"
              value={selectedType}
              onChange={(e) => setSelectedType(e.target.value)}
            >
              <option value="worktree_gc">Worktree Ephemeral GC</option>
              <option value="artifact_retention">Dead Artifact Retention Prune</option>
              <option value="index_rebuild">Vector / BM25 Index Rebuild</option>
            </select>
          </div>

          <div className="form-group flex-row items-center gap-2" style={{ marginTop: 'var(--space-3)' }}>
            <label className="flex-row items-center gap-2 text-xs cursor-pointer">
              <input
                type="checkbox"
                checked={isDryRun}
                onChange={(e) => setIsDryRun(e.target.checked)}
              />
              <span>Dry Run Simulation</span>
            </label>
          </div>

          <div style={{ marginTop: 'var(--space-3)' }}>
            <Button
              variant={isDryRun ? 'secondary' : 'primary'}
              size="sm"
              disabled={submitting}
              onClick={handleTriggerJob}
            >
              {submitting ? 'Running…' : isDryRun ? 'Simulate Dry Run' : 'Execute Maintenance'}
            </Button>
          </div>
        </div>
      </div>

      {/* Jobs History */}
      {loading ? (
        <LoadingState message="Auditing background maintenance jobs and reclamation history…" />
      ) : error ? (
        <ErrorState severity="error" message={error} onRetry={fetchJobs} />
      ) : jobs.length === 0 ? (
        <EmptyState title="No maintenance history" description="Trigger a dry-run or execution above." />
      ) : (
        <div className="table-responsive">
          <table className="data-table" aria-label="Maintenance Jobs Table">
            <thead>
              <tr>
                <th>Job ID</th>
                <th>Operation</th>
                <th>Mode</th>
                <th>Status</th>
                <th>Reclaimed</th>
                <th>Records</th>
                <th>Audit ID</th>
                <th>Executed</th>
              </tr>
            </thead>
            <tbody>
              {jobs.map((j) => (
                <tr key={j.job_id}>
                  <td>
                    <code className="font-mono text-xs font-bold">{j.job_id}</code>
                  </td>
                  <td>
                    <span className="font-semibold text-xs">{j.job_type.replace(/_/g, ' ').toUpperCase()}</span>
                  </td>
                  <td>
                    <span className={`diff-tag ${j.is_dry_run ? 'tag-modified' : 'tag-added'}`}>
                      {j.is_dry_run ? 'DRY-RUN' : 'LIVE'}
                    </span>
                  </td>
                  <td>
                    <StatusBadge
                      status={j.status === 'completed' || j.status === 'dry_run_ready' ? 'ready' : 'degraded'}
                      label={j.status.toUpperCase()}
                    />
                  </td>
                  <td className="font-mono text-xs">
                    {j.reclaimed_bytes > 0 ? `${(j.reclaimed_bytes / 1024 / 1024).toFixed(1)} MB` : '0 MB'}
                  </td>
                  <td className="font-mono text-xs">{j.records_affected}</td>
                  <td>
                    <code className="font-mono text-xs text-dim">{j.audit_id}</code>
                  </td>
                  <td className="font-mono text-xs text-dim">
                    {new Date(j.started_at).toLocaleTimeString()}
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
