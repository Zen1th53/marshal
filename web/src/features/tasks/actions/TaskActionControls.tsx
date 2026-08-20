import { useState } from 'react';
import { api } from '../../../api/client';
import { Button } from '../../../components/ui';
import { AuthorizedAction } from '../../../components/security/AuthorizedAction';
import { useToast } from '../../../components/toast';
import type { TaskStatus } from '../../../api/types';

interface TaskActionControlsProps {
  taskId: string;
  status: TaskStatus;
  onActionComplete: () => void;
}

export function TaskActionControls({ taskId, status, onActionComplete }: TaskActionControlsProps) {
  const [loading, setLoading] = useState(false);
  const { addToast } = useToast();

  const handleRun = async () => {
    setLoading(true);
    try {
      const run = await api.runTask(taskId);
      addToast({
        type: 'info',
        message: `Task run started: ${run.run_id}`,
      });
      onActionComplete();
    } catch (err: unknown) {
      addToast({
        type: 'error',
        message: err instanceof Error ? err.message : 'Failed to start run',
      });
    } finally {
      setLoading(false);
    }
  };

  const handleCancel = async () => {
    setLoading(true);
    try {
      await api.cancelTask(taskId);
      addToast({
        type: 'warning',
        message: `Task ${taskId} canceled.`,
      });
      onActionComplete();
    } catch (err: unknown) {
      addToast({
        type: 'error',
        message: err instanceof Error ? err.message : 'Failed to cancel task',
      });
    } finally {
      setLoading(false);
    }
  };

  const handleRetry = async () => {
    setLoading(true);
    try {
      const run = await api.retryTask(taskId);
      addToast({
        type: 'info',
        message: `Task retried: ${run.run_id}`,
      });
      onActionComplete();
    } catch (err: unknown) {
      addToast({
        type: 'error',
        message: err instanceof Error ? err.message : 'Failed to retry task',
      });
    } finally {
      setLoading(false);
    }
  };

  const handleClaim = async () => {
    setLoading(true);
    try {
      await api.claimTask(taskId);
      addToast({
        type: 'success',
        message: `Task ${taskId} claimed with lease.`,
      });
      onActionComplete();
    } catch (err: unknown) {
      addToast({
        type: 'error',
        message: err instanceof Error ? err.message : 'Failed to claim task',
      });
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="task-action-controls">
      {status === 'pending' && (
        <Button variant="secondary" size="sm" onClick={handleClaim} disabled={loading}>
          Claim Lease
        </Button>
      )}

      {(status === 'ready' || status === 'pending') && (
        <Button variant="primary" size="sm" onClick={handleRun} disabled={loading}>
          ▶ Start Run
        </Button>
      )}

      {status === 'running' && (
        <AuthorizedAction
          authority="task:cancel"
          onAction={handleCancel}
          isDestructive={true}
          confirmTitle="Confirm Task Cancellation"
          confirmMessage={`Are you sure you want to cancel executing task ${taskId}? In-flight worker operations will be halted.`}
          size="sm"
        >
          Cancel Task
        </AuthorizedAction>
      )}

      {(status === 'failed' || status === 'canceled') && (
        <Button variant="secondary" size="sm" onClick={handleRetry} disabled={loading}>
          ↻ Retry Task
        </Button>
      )}
    </div>
  );
}
