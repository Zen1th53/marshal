import { useAuth } from '../../auth/AuthContext';
import { useRealtimeStatus } from '../../realtime/store';
import { StatusBadge } from '../ui';

interface HeaderProps {
  onOpenCommandPalette: () => void;
}

export function Header({ onOpenCommandPalette }: HeaderProps) {
  const { user, logout } = useAuth();
  const { status: sseStatus } = useRealtimeStatus();

  return (
    <header className="app-header" role="banner">
      <div className="app-brand">
        <span className="app-logo" aria-hidden="true">◇</span>
        <h1 className="app-title">MARSHAL</h1>
        <span className="app-subtitle">Control Plane</span>
      </div>

      <div className="app-header-center">
        <button
          type="button"
          className="header-command-trigger"
          onClick={onOpenCommandPalette}
          aria-label="Open command palette"
        >
          <span className="search-icon" aria-hidden="true">🔍</span>
          <span className="command-placeholder">Search commands or routes…</span>
          <kbd className="command-kbd">⌘K</kbd>
        </button>
      </div>

      <div className="app-header-actions">
        <div className="realtime-status-pill" title={`SSE Stream: ${sseStatus}`}>
          <StatusBadge
            status={sseStatus === 'connected' ? 'ready' : sseStatus === 'reconnecting' ? 'degraded' : 'unavailable'}
            label={sseStatus === 'connected' ? 'Live' : sseStatus}
          />
        </div>

        {user && (
          <button
            type="button"
            className="btn btn-ghost btn-sm header-logout-btn"
            onClick={() => void logout()}
            aria-label="Logout operator"
          >
            Logout
          </button>
        )}
      </div>
    </header>
  );
}
