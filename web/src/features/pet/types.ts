export type MarshalPetState =
  | 'idle'
  | 'walking'
  | 'floating'
  | 'thinking'
  | 'working'
  | 'reading'
  | 'success'
  | 'warning'
  | 'error'
  | 'sleeping'
  | 'talking';

export type PetIntensity = 'low' | 'normal' | 'high';

export interface PetPosition {
  x: number;
  y: number;
}

export interface PetSettings {
  enabled: boolean;
  autonomousMovement: boolean;
  taskNotifications: boolean;
  securityNotifications: boolean;
  tipsAndSuggestions: boolean;
  soundEnabled: boolean;
  intensity: PetIntensity;
  restingPosition: PetPosition | null;
}

export const DEFAULT_PET_SETTINGS: PetSettings = {
  enabled: true,
  autonomousMovement: true,
  taskNotifications: true,
  securityNotifications: true,
  tipsAndSuggestions: false,
  soundEnabled: false,
  intensity: 'normal',
  restingPosition: null,
};

export type PetMessagePriority =
  | 'critical'   // Security alerts, system critical errors
  | 'warning'    // Resource pressure, task failure
  | 'success'    // Task completion, verification pass
  | 'info'       // Task progress, agent activity
  | 'tip'        // Helpful tips
  | 'idle';      // Friendly idle remarks

export interface PetSpeechMessage {
  id: string;
  text: string;
  priority: PetMessagePriority;
  durationMs?: number;
  action?: {
    label: string;
    route: string;
    targetId?: string;
  };
  createdAt: number;
}

export interface PetTarget {
  id: string;
  type: 'task' | 'agent' | 'security' | 'resource' | 'terminal' | 'general';
  element: HTMLElement;
  label?: string;
  route?: string;
}

export type PetEventType =
  | 'task_started'
  | 'task_progress'
  | 'task_completed'
  | 'task_failed'
  | 'agent_started'
  | 'agent_waiting'
  | 'security_alert'
  | 'resource_warning'
  | 'system_healthy'
  | 'deployment_started'
  | 'deployment_completed'
  | 'user_poke'
  | 'navigate';

export interface PetEvent<T = unknown> {
  type: PetEventType;
  data?: T;
  timestamp?: number;
}
