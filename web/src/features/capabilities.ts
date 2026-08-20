import { createContext, useContext } from 'react';
import type { CapabilityState, CapabilityStatusDTO } from '../api/types';

export interface CapabilitiesContextValue {
  capabilities: Record<string, CapabilityStatusDTO>;
  hasCapability: (capName: string) => boolean;
  getCapabilityState: (capName: string) => CapabilityState;
  getCapabilityReason: (capName: string) => string | undefined;
}

export const CapabilitiesContext = createContext<CapabilitiesContextValue>({
  capabilities: {},
  hasCapability: () => false,
  getCapabilityState: () => 'UNAVAILABLE',
  getCapabilityReason: () => undefined,
});

export function useCapabilities(): CapabilitiesContextValue {
  return useContext(CapabilitiesContext);
}
