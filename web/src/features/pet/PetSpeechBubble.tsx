import type { PetSpeechMessage } from './types';

interface PetSpeechBubbleProps {
  message: PetSpeechMessage | null;
  onDismiss: () => void;
  onAction?: (route: string, targetId?: string) => void;
}

export function PetSpeechBubble({ message, onDismiss, onAction }: PetSpeechBubbleProps) {
  if (!message) return null;

  const handleActionClick = (e: React.MouseEvent) => {
    e.stopPropagation();
    if (message.action && onAction) {
      onAction(message.action.route, message.action.targetId);
      onDismiss();
    }
  };

  const getPriorityLabel = () => {
    switch (message.priority) {
      case 'critical': return 'Security Alert';
      case 'warning': return 'Warning';
      case 'success': return 'Complete';
      case 'tip': return 'Tip';
      default: return 'MARSHAL';
    }
  };

  return (
    <div
      className={`marshal-pet-speech-bubble priority-${message.priority}`}
      role="status"
      aria-live="polite"
      onClick={(e) => e.stopPropagation()}
    >
      <div className="pet-bubble-header">
        <span>{getPriorityLabel()}</span>
        <button
          type="button"
          className="pet-bubble-dismiss"
          onClick={onDismiss}
          aria-label="Dismiss message"
        >
          ✕
        </button>
      </div>

      <p className="pet-bubble-body">{message.text}</p>

      {message.action && (
        <button
          type="button"
          className="pet-bubble-action"
          onClick={handleActionClick}
        >
          {message.action.label} →
        </button>
      )}
    </div>
  );
}
