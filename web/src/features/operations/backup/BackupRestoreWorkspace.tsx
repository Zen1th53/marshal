import { useState, useEffect, useCallback } from 'react';
import { api } from '../../../api/client';
import { StatusBadge, Button } from '../../../components/ui';
import { useToast } from '../../../components/toast';
import { LoadingState, ErrorState, EmptyState } from '../../../components/state';

interface BackupRecord {
  backup_id: string;
  schema_version: number;
  size_bytes: number;
  digest_sha256: string;
  status: string;
  created_at: string;
}

export function BackupRestoreWorkspace() {
  const [backups, setBackups] = useState<BackupRecord[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [creating, setCreating] = useState(false);
  const [restoringTarget, setRestoringTarget] = useState<BackupRecord | null>(null);
  const [restoring, setRestoring] = useState(false);
  const { addToast } = useToast();

  const fetchBackups = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const resp = await api.listBackups();
      setBackups(resp.backups);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to query backup records');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void fetchBackups();
  }, [fetchBackups]);

  const handleCreateBackup = async () => {
    setCreating(true);
    try {
      const resp = await api.createBackup({ label: 'manual-operator-checkpoint' });
      addToast({
        type: 'success',
        message: `Created verified state backup ${resp.backup_id}`,
      });
      await fetchBackups();
    } catch (err: unknown) {
      addToast({
        type: 'error',
        message: err instanceof Error ? err.message : 'Failed to create backup',
      });
    } finally {
      setCreating(false);
    }
  };

  const handleVerify = async (b: BackupRecord) => {
    try {
      const resp = await api.verifyBackup(b.backup_id);
      addToast({
        type: 'info',
        message: `Backup ${resp.backup_id} integrity verified: ${resp.integrity_status}`,
      });
    } catch (err: unknown) {
      addToast({
        type: 'error',
        message: err instanceof Error ? err.message : 'Integrity verification failed',
      });
    }
  };

  const handleRestore = async () => {
    if (!restoringTarget) return;
    setRestoring(true);
    try {
      const resp = await api.restoreBackup({
        backup_id: restoringTarget.backup_id,
        expected_digest_sha256: restoringTarget.digest_sha256,
        safety_backup_label: 'pre-restore-safety-snapshot',
      });
      addToast({
        type: 'success',
        message: `State restored to ${resp.restored_backup_id} (Safety snapshot: ${resp.safety_backup_id})`,
      });
      setRestoringTarget(null);
      await fetchBackups();
    } catch (err: unknown) {
      addToast({
        type: 'error',
        message: err instanceof Error ? err.message : 'Restore operation failed',
      });
    } finally {
      setRestoring(false);
    }
  };

  return (
    <div className="backup-restore-container">
      <div className="flex-row items-center justify-between" style={{ marginBottom: 'var(--space-3)' }}>
        <div>
          <h3 className="text-base font-semibold">State Backups, Integrity Preflight & Safe Restore</h3>
          <span className="text-xs text-dim">
            SQLite database WAL and memory corpus atomic snapshots with automatic pre-restore safety backups
          </span>
        </div>
        <Button variant="primary" size="sm" disabled={creating} onClick={handleCreateBackup}>
          {creating ? 'Creating…' : 'Create State Backup'}
        </Button>
      </div>

      {loading ? (
        <LoadingState message="Auditing backup catalog and SHA-256 integrity digests…" />
      ) : error ? (
        <ErrorState severity="error" message={error} onRetry={fetchBackups} />
      ) : backups.length === 0 ? (
        <EmptyState
          title="No backups found"
          description="Create a verified snapshot of SQLite and memory state."
        />
      ) : (
        <div className="table-responsive">
          <table className="data-table" aria-label="State Backups Table">
            <thead>
              <tr>
                <th>Backup ID</th>
                <th>Schema</th>
                <th>Size</th>
                <th>SHA-256 Digest</th>
                <th>Status</th>
                <th>Created</th>
                <th>Actions</th>
              </tr>
            </thead>
            <tbody>
              {backups.map((b) => (
                <tr key={b.backup_id}>
                  <td>
                    <code className="font-mono text-xs font-bold">{b.backup_id}</code>
                  </td>
                  <td className="font-mono text-xs">v{b.schema_version}</td>
                  <td className="font-mono text-xs">{(b.size_bytes / 1024 / 1024).toFixed(2)} MB</td>
                  <td>
                    <code className="font-mono text-xs text-dim">
                      {b.digest_sha256.slice(0, 16)}…
                    </code>
                  </td>
                  <td>
                    <StatusBadge
                      status={b.status === 'verified' ? 'ready' : 'degraded'}
                      label={b.status.toUpperCase()}
                    />
                  </td>
                  <td className="font-mono text-xs text-dim">
                    {new Date(b.created_at).toLocaleTimeString()}
                  </td>
                  <td>
                    <div className="flex-row items-center gap-2">
                      <Button variant="ghost" size="sm" onClick={() => handleVerify(b)}>
                        Verify
                      </Button>
                      <Button variant="secondary" size="sm" onClick={() => setRestoringTarget(b)}>
                        Restore
                      </Button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {/* Restore Confirmation Modal */}
      {restoringTarget && (
        <div className="modal-backdrop" onClick={() => setRestoringTarget(null)}>
          <div className="modal-card" onClick={(e) => e.stopPropagation()}>
            <div className="modal-header">
              <h3 className="modal-title">Confirm State Restoration</h3>
              <button
                type="button"
                className="btn btn-ghost btn-sm"
                onClick={() => setRestoringTarget(null)}
                aria-label="Close"
              >
                ✕
              </button>
            </div>

            <div className="modal-body">
              <div className="alert-box alert-warning" style={{ marginBottom: 'var(--space-3)' }}>
                <strong>Safety Precaution:</strong> A pre-restore safety snapshot will automatically be created prior to applying this backup.
              </div>

              <div className="form-group" style={{ marginBottom: 'var(--space-2)' }}>
                <span className="text-xs text-dim">Target Backup ID:</span>
                <code className="font-mono text-xs block">{restoringTarget.backup_id}</code>
              </div>

              <div className="form-group">
                <span className="text-xs text-dim">Expected Digest SHA-256:</span>
                <code className="font-mono text-xs block text-dim">{restoringTarget.digest_sha256}</code>
              </div>
            </div>

            <div className="modal-actions">
              <Button variant="secondary" size="sm" onClick={() => setRestoringTarget(null)}>
                Cancel
              </Button>
              <Button
                variant="danger"
                size="sm"
                disabled={restoring}
                onClick={handleRestore}
              >
                {restoring ? 'Restoring State…' : 'Proceed with Restore'}
              </Button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
