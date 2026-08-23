import { realtime, type SSEMessageEvent } from '../../realtime/client';
import type { PetEvent, PetSpeechMessage, PetMessagePriority } from './types';
import { PetStateMachine } from './PetStateMachine';
import { petAudio } from './PetAudio';
import { loadPetSettings } from './petStore';

export type SpeechMessageListener = (msg: PetSpeechMessage | null) => void;
export type PetEventListener = (event: PetEvent) => void;

const PRIORITY_SCORES: Record<PetMessagePriority, number> = {
  critical: 100,
  warning: 80,
  success: 60,
  info: 40,
  tip: 20,
  idle: 10,
};

export class PetEventBridge {
  private stateMachine: PetStateMachine;
  private currentSpeech: PetSpeechMessage | null = null;
  private speechTimer: ReturnType<typeof setTimeout> | null = null;
  private speechListeners = new Set<SpeechMessageListener>();
  private eventListeners = new Set<PetEventListener>();
  private sseUnsubscribe: (() => void) | null = null;

  constructor(stateMachine: PetStateMachine) {
    this.stateMachine = stateMachine;
    this.initSSEBridge();
  }

  private initSSEBridge(): void {
    if (typeof window === 'undefined') return;

    this.sseUnsubscribe = realtime.subscribe('*', (ev: SSEMessageEvent) => {
      this.handleSSEEvent(ev);
    });
  }

  private handleSSEEvent(ev: SSEMessageEvent): void {
    const settings = loadPetSettings();
    if (!settings.enabled) return;

    switch (ev.type) {
      case 'task.status': {
        const payload = (ev.data || {}) as { task_id?: string; status?: string; agent_id?: string; title?: string };
        const status = (payload.status || '').toLowerCase();
        const taskId = payload.task_id || ev.scope_id || 'Task';

        if (status === 'running' || status === 'in_progress' || status === 'claimed') {
          this.emit({ type: 'task_started', data: payload });
          if (settings.taskNotifications) {
            this.speak({
              id: `task-start-${Date.now()}`,
              text: `Working on ${taskId}...`,
              priority: 'info',
              durationMs: 4500,
              action: { label: 'View Tasks', route: 'tasks', targetId: taskId },
              createdAt: Date.now(),
            });
            this.stateMachine.transitionTo('working', 6000);
            if (settings.soundEnabled) petAudio.playChirp('work');
          }
        } else if (status === 'completed' || status === 'passed' || status === 'done') {
          this.emit({ type: 'task_completed', data: payload });
          if (settings.taskNotifications) {
            this.speak({
              id: `task-done-${Date.now()}`,
              text: `${taskId} completed! ✓`,
              priority: 'success',
              durationMs: 5000,
              action: { label: 'Review Output', route: 'review', targetId: taskId },
              createdAt: Date.now(),
            });
            this.stateMachine.transitionTo('success', 4000);
            if (settings.soundEnabled) petAudio.playChirp('happy');
          }
        } else if (status === 'failed' || status === 'error' || status === 'rejected') {
          this.emit({ type: 'task_failed', data: payload });
          if (settings.taskNotifications) {
            this.speak({
              id: `task-fail-${Date.now()}`,
              text: `${taskId} reported an error.`,
              priority: 'warning',
              durationMs: 6000,
              action: { label: 'Inspect Logs', route: 'runs', targetId: taskId },
              createdAt: Date.now(),
            });
            this.stateMachine.transitionTo('warning', 5000);
            if (settings.soundEnabled) petAudio.playChirp('alert');
          }
        }
        break;
      }

      case 'audit.log': {
        const payload = (ev.data || {}) as { action?: string; severity?: string; message?: string };
        if (payload.severity === 'CRITICAL' || payload.severity === 'HIGH' || payload.action?.includes('security')) {
          this.emit({ type: 'security_alert', data: payload });
          if (settings.securityNotifications) {
            this.speak({
              id: `sec-${Date.now()}`,
              text: `Security alert: ${payload.action || 'Policy violation'}`,
              priority: 'critical',
              durationMs: 7000,
              action: { label: 'Open Security', route: 'security' },
              createdAt: Date.now(),
            });
            this.stateMachine.transitionTo('warning', 6000);
            if (settings.soundEnabled) petAudio.playChirp('alert');
          }
        }
        break;
      }

      case 'review.status': {
        const payload = (ev.data || {}) as { task_id?: string; verdict?: string };
        if (payload.verdict === 'APPROVED') {
          this.speak({
            id: `review-${Date.now()}`,
            text: `Review approved for ${payload.task_id || 'task'}`,
            priority: 'success',
            durationMs: 4000,
            action: { label: 'View Review', route: 'review' },
            createdAt: Date.now(),
          });
          this.stateMachine.transitionTo('success', 3000);
        }
        break;
      }

      case 'memory.mutated': {
        this.stateMachine.transitionTo('thinking', 2500);
        break;
      }
    }
  }

  emit(event: PetEvent): void {
    const enriched: PetEvent = { ...event, timestamp: event.timestamp || Date.now() };
    for (const listener of this.eventListeners) {
      try {
        listener(enriched);
      } catch {
        // Safe execution
      }
    }
  }

  speak(message: PetSpeechMessage): void {
    // Priority preemption check
    if (this.currentSpeech) {
      const currentScore = PRIORITY_SCORES[this.currentSpeech.priority] || 0;
      const newScore = PRIORITY_SCORES[message.priority] || 0;
      if (newScore < currentScore) {
        // Lower priority message cannot preempt an active higher priority message
        return;
      }
    }

    if (this.speechTimer) {
      clearTimeout(this.speechTimer);
      this.speechTimer = null;
    }

    this.currentSpeech = message;
    this.notifySpeech(message);

    const duration = message.durationMs || 5000;
    this.speechTimer = setTimeout(() => {
      this.dismissSpeech();
    }, duration);
  }

  dismissSpeech(): void {
    if (this.speechTimer) {
      clearTimeout(this.speechTimer);
      this.speechTimer = null;
    }
    this.currentSpeech = null;
    this.notifySpeech(null);
  }

  getCurrentSpeech(): PetSpeechMessage | null {
    return this.currentSpeech;
  }

  onSpeechChange(listener: SpeechMessageListener): () => void {
    this.speechListeners.add(listener);
    return () => this.speechListeners.delete(listener);
  }

  onEvent(listener: PetEventListener): () => void {
    this.eventListeners.add(listener);
    return () => this.eventListeners.delete(listener);
  }

  private notifySpeech(msg: PetSpeechMessage | null): void {
    for (const listener of this.speechListeners) {
      try {
        listener(msg);
      } catch {
        // Safe execution
      }
    }
  }

  destroy(): void {
    if (this.speechTimer) {
      clearTimeout(this.speechTimer);
      this.speechTimer = null;
    }
    if (this.sseUnsubscribe) {
      this.sseUnsubscribe();
      this.sseUnsubscribe = null;
    }
    this.speechListeners.clear();
    this.eventListeners.clear();
  }
}
