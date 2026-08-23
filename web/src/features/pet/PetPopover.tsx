import type { MarshalPetState } from './types';
import { usePetSettings } from './petStore';

interface PetPopoverProps {
  isOpen: boolean;
  onClose: () => void;
  state: MarshalPetState;
  onStateChange: (state: MarshalPetState) => void;
  onNavigate?: (route: string) => void;
  onResetPosition: () => void;
}

export function PetPopover({
  isOpen,
  onClose,
  state,
  onStateChange,
  onNavigate,
  onResetPosition,
}: PetPopoverProps) {
  const { settings, updateSettings } = usePetSettings();

  if (!isOpen) return null;

  const handleNavigate = (route: string) => {
    if (onNavigate) {
      onNavigate(route);
    }
    onClose();
  };

  const toggleSleep = () => {
    if (state === 'sleeping') {
      onStateChange('idle');
    } else {
      onStateChange('sleeping');
    }
  };

  const toggleRoaming = () => {
    updateSettings({ autonomousMovement: !settings.autonomousMovement });
  };

  return (
    <div
      className="marshal-pet-popover"
      role="dialog"
      aria-label="MARSHAL Assistant Menu"
      onClick={(e) => e.stopPropagation()}
    >
      <div className="pet-popover-header">
        <div className="pet-popover-title">
          <span className="pet-popover-status-dot" />
          <span>MARSHAL Companion</span>
        </div>
        <button
          type="button"
          className="pet-bubble-dismiss"
          onClick={onClose}
          aria-label="Close Assistant Menu"
        >
          ✕
        </button>
      </div>

      <div style={{ display: 'flex', flexDirection: 'column', gap: '2px' }}>
        <button
          type="button"
          className="pet-popover-menu-item"
          onClick={() => handleNavigate('tasks')}
        >
          <span>📋</span>
          <span>Tasks & Workflows</span>
        </button>

        <button
          type="button"
          className="pet-popover-menu-item"
          onClick={() => handleNavigate('operations')}
        >
          <span>⚙</span>
          <span>System & Resources</span>
        </button>

        <button
          type="button"
          className="pet-popover-menu-item"
          onClick={() => handleNavigate('security')}
        >
          <span>🛡</span>
          <span>Security & Boundary</span>
        </button>

        <hr style={{ borderColor: 'var(--color-border, #2e2e36)', margin: '4px 0' }} />

        <button
          type="button"
          className="pet-popover-menu-item"
          onClick={toggleSleep}
        >
          <span>{state === 'sleeping' ? '☀️' : '🌙'}</span>
          <span>{state === 'sleeping' ? 'Wake Up' : 'Go to Sleep'}</span>
        </button>

        <button
          type="button"
          className="pet-popover-menu-item"
          onClick={toggleRoaming}
        >
          <span>{settings.autonomousMovement ? '⏸' : '▶'}</span>
          <span>{settings.autonomousMovement ? 'Pause Roaming' : 'Resume Roaming'}</span>
        </button>

        <button
          type="button"
          className="pet-popover-menu-item"
          onClick={() => {
            onResetPosition();
            onClose();
          }}
        >
          <span>📍</span>
          <span>Reset Resting Position</span>
        </button>

        <button
          type="button"
          className="pet-popover-menu-item"
          onClick={() => handleNavigate('settings')}
        >
          <span>🔧</span>
          <span>Companion Settings</span>
        </button>
      </div>
    </div>
  );
}
