import { useToast } from './ToastContext';

export function ToastContainer() {
  const { toasts, removeToast } = useToast();

  if (toasts.length === 0) return null;

  return (
    <div className="toast-container" role="region" aria-label="Notifications" aria-live="polite">
      {toasts.map((toast) => (
        <div key={toast.id} className={`toast-item toast-${toast.type}`} role="alert">
          <span className="toast-message">{toast.message}</span>
          {toast.correlationId && (
            <code className="toast-correlation">[{toast.correlationId}]</code>
          )}
          <button
            type="button"
            className="toast-close-btn"
            onClick={() => removeToast(toast.id)}
            aria-label="Dismiss notification"
          >
            ✕
          </button>
        </div>
      ))}
    </div>
  );
}
