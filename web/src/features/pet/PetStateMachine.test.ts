import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { PetStateMachine } from './PetStateMachine';
import type { MarshalPetState } from './types';

describe('PetStateMachine', () => {
  let sm: PetStateMachine;

  beforeEach(() => {
    vi.useFakeTimers();
    sm = new PetStateMachine();
  });

  afterEach(() => {
    sm.destroy();
    vi.useRealTimers();
  });

  it('initializes in idle state', () => {
    expect(sm.getState()).toBe('idle');
    expect(sm.getPreviousState()).toBe('idle');
  });

  it('transitions state and notifies listeners', () => {
    const listener = vi.fn();
    sm.onStateChange(listener);

    sm.transitionTo('working');
    expect(sm.getState()).toBe('working');
    expect(sm.getPreviousState()).toBe('idle');
    expect(listener).toHaveBeenCalledWith('working', 'idle');
  });

  it('automatically returns to idle after autoReturnDurationMs', () => {
    const transitions: MarshalPetState[] = [];
    sm.onStateChange((s) => transitions.push(s));

    sm.transitionTo('success', 3000);
    expect(sm.getState()).toBe('success');

    vi.advanceTimersByTime(2999);
    expect(sm.getState()).toBe('success');

    vi.advanceTimersByTime(2);
    expect(sm.getState()).toBe('idle');
    expect(transitions).toEqual(['success', 'idle']);
  });

  it('unsubscribes listeners cleanly', () => {
    const listener = vi.fn();
    const unsub = sm.onStateChange(listener);

    sm.transitionTo('thinking');
    expect(listener).toHaveBeenCalledTimes(1);

    unsub();
    sm.transitionTo('sleeping');
    expect(listener).toHaveBeenCalledTimes(1);
  });
});
