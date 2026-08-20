import { useEffect } from 'react';
import { realtime, type SSEEventHandler } from './client';

export function useRealtimeEvent<T = unknown>(
  eventType: string,
  handler: SSEEventHandler<T>,
  deps: React.DependencyList = []
): void {
  useEffect(() => {
    const unsubscribe = realtime.subscribe<T>(eventType, handler);
    return () => {
      unsubscribe();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [eventType, ...deps]);
}

export function useRealtimeResync(onResync: (reason?: string) => void): void {
  useEffect(() => {
    const unsubscribe = realtime.onResync(onResync);
    return () => {
      unsubscribe();
    };
  }, [onResync]);
}
