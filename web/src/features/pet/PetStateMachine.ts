import type { MarshalPetState } from './types';

export type StateChangeListener = (newState: MarshalPetState, prevState: MarshalPetState) => void;

export class PetStateMachine {
  private currentState: MarshalPetState = 'idle';
  private previousState: MarshalPetState = 'idle';
  private stateTimer: ReturnType<typeof setTimeout> | null = null;
  private listeners = new Set<StateChangeListener>();

  getState(): MarshalPetState {
    return this.currentState;
  }

  getPreviousState(): MarshalPetState {
    return this.previousState;
  }

  transitionTo(nextState: MarshalPetState, autoReturnDurationMs?: number): void {
    if (this.stateTimer) {
      clearTimeout(this.stateTimer);
      this.stateTimer = null;
    }

    if (this.currentState === nextState && !autoReturnDurationMs) {
      return;
    }

    const prev = this.currentState;
    this.previousState = prev;
    this.currentState = nextState;

    this.notify(nextState, prev);

    if (autoReturnDurationMs && autoReturnDurationMs > 0) {
      this.stateTimer = setTimeout(() => {
        this.transitionTo('idle');
      }, autoReturnDurationMs);
    }
  }

  onStateChange(listener: StateChangeListener): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  private notify(newState: MarshalPetState, prevState: MarshalPetState): void {
    for (const listener of this.listeners) {
      try {
        listener(newState, prevState);
      } catch {
        // Safe execution
      }
    }
  }

  destroy(): void {
    if (this.stateTimer) {
      clearTimeout(this.stateTimer);
      this.stateTimer = null;
    }
    this.listeners.clear();
  }
}
