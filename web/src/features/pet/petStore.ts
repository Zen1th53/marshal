import { useState, useEffect, useCallback } from 'react';
import { DEFAULT_PET_SETTINGS, type PetSettings, type PetPosition } from './types';

const STORAGE_KEY_SETTINGS = 'marshal_pet_settings';

export function loadPetSettings(): PetSettings {
  if (typeof window === 'undefined') return DEFAULT_PET_SETTINGS;
  try {
    const raw = localStorage.getItem(STORAGE_KEY_SETTINGS);
    if (!raw) return DEFAULT_PET_SETTINGS;
    return { ...DEFAULT_PET_SETTINGS, ...JSON.parse(raw) };
  } catch {
    return DEFAULT_PET_SETTINGS;
  }
}

export function savePetSettings(settings: PetSettings): void {
  if (typeof window === 'undefined') return;
  try {
    localStorage.setItem(STORAGE_KEY_SETTINGS, JSON.stringify(settings));
    window.dispatchEvent(new CustomEvent('marshal_pet_settings_changed', { detail: settings }));
  } catch {
    // Storage quota or disabled
  }
}

export function savePetRestingPosition(position: PetPosition): void {
  const current = loadPetSettings();
  savePetSettings({ ...current, restingPosition: position });
}

export function usePetSettings() {
  const [settings, setSettings] = useState<PetSettings>(loadPetSettings);

  useEffect(() => {
    const handleUpdate = (e: Event) => {
      const custom = e as CustomEvent<PetSettings>;
      if (custom.detail) {
        setSettings(custom.detail);
      } else {
        setSettings(loadPetSettings());
      }
    };
    window.addEventListener('marshal_pet_settings_changed', handleUpdate);
    window.addEventListener('storage', handleUpdate);
    return () => {
      window.removeEventListener('marshal_pet_settings_changed', handleUpdate);
      window.removeEventListener('storage', handleUpdate);
    };
  }, []);

  const updateSettings = useCallback((partial: Partial<PetSettings>) => {
    setSettings((prev) => {
      const next = { ...prev, ...partial };
      savePetSettings(next);
      return next;
    });
  }, []);

  return { settings, updateSettings };
}
