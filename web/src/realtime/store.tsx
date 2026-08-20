import { createContext, useContext, useEffect, useState, type ReactNode } from 'react';
import { realtime, type SSEConnectionState } from './client';

export interface RealtimeContextValue {
  status: SSEConnectionState;
  lastEventId: number;
}

export const RealtimeContext = createContext<RealtimeContextValue>({
  status: 'disconnected',
  lastEventId: 0,
});

export function RealtimeProvider({ children }: { children: ReactNode }) {
  const [status, setStatus] = useState<SSEConnectionState>(realtime.getStatus());
  const [lastEventId, setLastEventId] = useState(realtime.getLastEventId());

  useEffect(() => {
    realtime.connect();
    const unsubStatus = realtime.onStatusChange(setStatus);
    const unsubEvents = realtime.subscribe('*', (ev) => {
      setLastEventId(ev.id);
    });

    return () => {
      unsubStatus();
      unsubEvents();
      realtime.disconnect();
    };
  }, []);

  return (
    <RealtimeContext.Provider value={{ status, lastEventId }}>
      {children}
    </RealtimeContext.Provider>
  );
}

export function useRealtimeStatus(): RealtimeContextValue {
  return useContext(RealtimeContext);
}
