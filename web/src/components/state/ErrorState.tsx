import { Button } from '../ui';

export type ErrorSeverity = 'error' | 'unauthorized' | 'degraded' | 'unavailable';

interface ErrorStateProps {
  severity?: ErrorSeverity;
  title?: string;
  message?: string;
  correlationId?: string;
  onRetry?: () => void;
}

export function ErrorState({
  severity = 'error',
  title,
  message,
  correlationId,
  onRetry,
}: ErrorStateProps) {
  const defaultTitle =
    severity === 'unauthorized'
      ? 'Access Restricted'
      : severity === 'unavailable'
      ? 'Service Unavailable'
      : severity === 'degraded'
      ? 'Service Degraded'
      : 'Operation Failed';

  const defaultMessage =
    severity === 'unauthorized'
      ? 'You do not have the required authority to view or mutate this resource.'
      : 'An unexpected error occurred while communicating with the MARSHAL control plane.';

  return (
    <div className={`state-view state-error state-severity-${severity}`} role="alert">
      <div className="error-icon" aria-hidden="true">
        {severity === 'unauthorized' ? '🔒' : severity === 'degraded' ? '▲' : '✕'}
      </div>
      <h3 className="state-title">{title ?? defaultTitle}</h3>
      <p className="state-message">{message ?? defaultMessage}</p>
      {correlationId && (
        <p className="state-correlation">
          <span className="state-correlation-label">Reference ID:</span>{' '}
          <code>{correlationId}</code>
        </p>
      )}
      {onRetry && (
        <div className="state-action">
          <Button variant="secondary" size="sm" onClick={onRetry}>
            Retry Operation
          </Button>
        </div>
      )}
    </div>
  );
}
