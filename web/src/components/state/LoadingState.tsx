interface LoadingStateProps {
  message?: string;
}

export function LoadingState({ message = 'Loading…' }: LoadingStateProps) {
  return (
    <div className="state-view state-loading" role="status" aria-live="polite" aria-busy="true">
      <div className="spinner" aria-hidden="true" />
      <p className="state-message">{message}</p>
    </div>
  );
}
