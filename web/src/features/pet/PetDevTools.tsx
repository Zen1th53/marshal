import { useState } from 'react';
import type { MarshalPetState } from './types';
import type { PetEventBridge } from './PetEventBridge';

interface PetDevToolsProps {
  bridge: PetEventBridge;
  onStateChange: (state: MarshalPetState) => void;
}

export function PetDevTools({ bridge, onStateChange }: PetDevToolsProps) {
  const [isOpen, setIsOpen] = useState(false);

  // Only active in DEV mode
  if (!import.meta.env.DEV) return null;

  if (!isOpen) {
    return (
      <button
        type="button"
        style={{
          position: 'fixed',
          bottom: '8px',
          left: '8px',
          zIndex: 10010,
          background: '#222228',
          border: '1px solid #444',
          color: '#aaa',
          fontSize: '10px',
          padding: '2px 6px',
          borderRadius: '4px',
          cursor: 'pointer',
          opacity: 0.7,
        }}
        onClick={() => setIsOpen(true)}
      >
        🤖 Pet DevTools
      </button>
    );
  }

  const triggerState = (s: MarshalPetState) => {
    onStateChange(s);
  };

  const triggerTaskEvent = () => {
    bridge.speak({
      id: `dev-task-${Date.now()}`,
      text: 'Executing task TASK-999 with 2 workers.',
      priority: 'info',
      durationMs: 4000,
      action: { label: 'View Tasks', route: 'tasks' },
      createdAt: Date.now(),
    });
    onStateChange('working');
  };

  const triggerSuccessEvent = () => {
    bridge.speak({
      id: `dev-success-${Date.now()}`,
      text: 'Build & tests passed! Release gate ready. ✓',
      priority: 'success',
      durationMs: 4000,
      action: { label: 'Review Output', route: 'review' },
      createdAt: Date.now(),
    });
    onStateChange('success');
  };

  const triggerSecurityAlert = () => {
    bridge.speak({
      id: `dev-sec-${Date.now()}`,
      text: 'Security policy violation detected: sandbox breach attempt.',
      priority: 'critical',
      durationMs: 6000,
      action: { label: 'View Security', route: 'security' },
      createdAt: Date.now(),
    });
    onStateChange('warning');
  };

  const triggerResourceWarning = () => {
    bridge.speak({
      id: `dev-res-${Date.now()}`,
      text: 'Host RAM pressure high (>85% utilized).',
      priority: 'warning',
      durationMs: 5000,
      action: { label: 'View Resources', route: 'operations' },
      createdAt: Date.now(),
    });
    onStateChange('warning');
  };

  return (
    <div className="pet-dev-panel">
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <strong style={{ fontSize: '11px', color: '#fff' }}>MARSHAL Pet DevTools</strong>
        <button
          type="button"
          onClick={() => setIsOpen(false)}
          style={{ background: 'none', border: 'none', color: '#888', cursor: 'pointer' }}
        >
          ✕
        </button>
      </div>

      <div className="pet-dev-grid">
        <button type="button" className="pet-dev-btn" onClick={() => triggerState('idle')}>Idle</button>
        <button type="button" className="pet-dev-btn" onClick={() => triggerState('thinking')}>Thinking</button>
        <button type="button" className="pet-dev-btn" onClick={() => triggerState('working')}>Working</button>
        <button type="button" className="pet-dev-btn" onClick={() => triggerState('reading')}>Reading</button>
        <button type="button" className="pet-dev-btn" onClick={() => triggerState('success')}>Success</button>
        <button type="button" className="pet-dev-btn" onClick={() => triggerState('warning')}>Warning</button>
        <button type="button" className="pet-dev-btn" onClick={() => triggerState('error')}>Error</button>
        <button type="button" className="pet-dev-btn" onClick={() => triggerState('sleeping')}>Sleep</button>
        <button type="button" className="pet-dev-btn" onClick={() => triggerState('talking')}>Talk</button>
        <button type="button" className="pet-dev-btn" onClick={triggerTaskEvent}>Event: Task</button>
        <button type="button" className="pet-dev-btn" onClick={triggerSuccessEvent}>Event: Success</button>
        <button type="button" className="pet-dev-btn" onClick={triggerSecurityAlert}>Event: Security</button>
        <button type="button" className="pet-dev-btn" onClick={triggerResourceWarning}>Event: RAM Warn</button>
      </div>
    </div>
  );
}
