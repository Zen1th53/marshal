import type { PetTarget } from './types';

class TargetRegistry {
  private targets = new Map<string, PetTarget>();
  private listeners = new Set<() => void>();

  register(target: PetTarget): () => void {
    this.targets.set(target.id, target);
    this.notify();
    return () => {
      this.unregister(target.id);
    };
  }

  unregister(id: string): void {
    if (this.targets.delete(id)) {
      this.notify();
    }
  }

  getTarget(id: string): PetTarget | undefined {
    return this.targets.get(id);
  }

  getTargetsByType(type: PetTarget['type']): PetTarget[] {
    return Array.from(this.targets.values()).filter((t) => t.type === type);
  }

  getAllTargets(): PetTarget[] {
    return Array.from(this.targets.values());
  }

  getRandomTarget(): PetTarget | null {
    const list = this.getAllTargets();
    if (list.length === 0) return null;
    return list[Math.floor(Math.random() * list.length)];
  }

  subscribe(listener: () => void): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  private notify(): void {
    for (const listener of this.listeners) {
      try {
        listener();
      } catch {
        // Safe execution
      }
    }
  }
}

export const petTargetRegistry = new TargetRegistry();
