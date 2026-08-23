import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { PetEventBridge } from './PetEventBridge';
import { PetStateMachine } from './PetStateMachine';
import type { PetSpeechMessage } from './types';

describe('PetEventBridge', () => {
  let sm: PetStateMachine;
  let bridge: PetEventBridge;

  beforeEach(() => {
    vi.useFakeTimers();
    sm = new PetStateMachine();
    bridge = new PetEventBridge(sm);
  });

  afterEach(() => {
    bridge.destroy();
    sm.destroy();
    vi.useRealTimers();
  });

  it('dispatches speech messages to listeners', () => {
    const listener = vi.fn();
    bridge.onSpeechChange(listener);

    const msg: PetSpeechMessage = {
      id: 'test-1',
      text: 'Hello, operator!',
      priority: 'info',
      durationMs: 3000,
      createdAt: Date.now(),
    };

    bridge.speak(msg);
    expect(listener).toHaveBeenCalledWith(msg);
    expect(bridge.getCurrentSpeech()).toEqual(msg);

    // Auto-dismisses after duration
    vi.advanceTimersByTime(3001);
    expect(bridge.getCurrentSpeech()).toBeNull();
    expect(listener).toHaveBeenLastCalledWith(null);
  });

  it('allows higher priority messages to preempt lower priority messages', () => {
    const lowMsg: PetSpeechMessage = {
      id: 'low-1',
      text: 'Random idle chatter',
      priority: 'idle',
      durationMs: 5000,
      createdAt: Date.now(),
    };

    const critMsg: PetSpeechMessage = {
      id: 'crit-1',
      text: 'Critical security violation!',
      priority: 'critical',
      durationMs: 5000,
      createdAt: Date.now(),
    };

    bridge.speak(lowMsg);
    expect(bridge.getCurrentSpeech()?.id).toBe('low-1');

    // High priority preempts low priority
    bridge.speak(critMsg);
    expect(bridge.getCurrentSpeech()?.id).toBe('crit-1');

    // Lower priority cannot preempt active critical message
    bridge.speak(lowMsg);
    expect(bridge.getCurrentSpeech()?.id).toBe('crit-1');
  });

  it('emits and listens to custom events', () => {
    const listener = vi.fn();
    bridge.onEvent(listener);

    bridge.emit({ type: 'task_started', data: { task_id: 'TASK-123' } });
    expect(listener).toHaveBeenCalledWith(expect.objectContaining({
      type: 'task_started',
      data: { task_id: 'TASK-123' },
    }));
  });
});
