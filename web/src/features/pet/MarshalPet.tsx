import { useState, useEffect, useRef, useCallback, useMemo } from 'react';
import type { MarshalPetState, PetPosition, PetSpeechMessage } from './types';
import { PetStateMachine } from './PetStateMachine';
import { PetEventBridge } from './PetEventBridge';
import { petTargetRegistry } from './PetTargetRegistry';
import { usePetSettings, savePetRestingPosition } from './petStore';
import { PetRenderer } from './PetRenderer';
import { PetSpeechBubble } from './PetSpeechBubble';
import { PetPopover } from './PetPopover';
import { PetDevTools } from './PetDevTools';
import { petAudio } from './PetAudio';
import './pet.css';

interface MarshalPetProps {
  onNavigate?: (route: string) => void;
}

const PET_WIDTH = 72;
const PET_HEIGHT = 80;
const PADDING = 20;

export function MarshalPet({ onNavigate }: MarshalPetProps) {
  const { settings } = usePetSettings();
  const stateMachine = useMemo(() => new PetStateMachine(), []);
  const eventBridge = useMemo(() => new PetEventBridge(stateMachine), [stateMachine]);

  const [state, setState] = useState<MarshalPetState>('idle');
  const [position, setPosition] = useState<PetPosition>(() => {
    if (settings.restingPosition) return settings.restingPosition;
    if (typeof window !== 'undefined') {
      return {
        x: Math.max(PADDING, window.innerWidth - PET_WIDTH - PADDING - 40),
        y: Math.max(PADDING, window.innerHeight - PET_HEIGHT - PADDING - 40),
      };
    }
    return { x: 100, y: 100 };
  });

  const [speech, setSpeech] = useState<PetSpeechMessage | null>(null);
  const [isPopoverOpen, setIsPopoverOpen] = useState(false);
  const [isDragging, setIsDragging] = useState(false);
  const [lookDirection, setLookDirection] = useState<'left' | 'right' | 'center'>('center');
  const [isBlinking, setIsBlinking] = useState(false);
  const [isReducedMotion, setIsReducedMotion] = useState(false);

  const containerRef = useRef<HTMLDivElement>(null);
  const dragStartRef = useRef<{ pointerX: number; pointerY: number; petX: number; petY: number } | null>(null);
  const movementTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const sleepTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const hasMovedSinceDragRef = useRef(false);

  // Sync state machine updates to React state
  useEffect(() => {
    const unsubState = stateMachine.onStateChange((newState) => {
      setState(newState);
    });
    const unsubSpeech = eventBridge.onSpeechChange((msg) => {
      setSpeech(msg);
    });
    return () => {
      unsubState();
      unsubSpeech();
      stateMachine.destroy();
      eventBridge.destroy();
    };
  }, [stateMachine, eventBridge]);

  // Reduced motion media query check
  useEffect(() => {
    if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return;
    const mediaQuery = window.matchMedia('(prefers-reduced-motion: reduce)');
    setIsReducedMotion(mediaQuery.matches);
    const handler = (e: MediaQueryListEvent) => setIsReducedMotion(e.matches);
    mediaQuery.addEventListener('change', handler);
    return () => mediaQuery.removeEventListener('change', handler);
  }, []);

  // Blinking animation loop
  useEffect(() => {
    if (state === 'sleeping') return;
    const blinkInterval = setInterval(() => {
      setIsBlinking(true);
      setTimeout(() => setIsBlinking(false), 180);
    }, 4000 + Math.random() * 3000);
    return () => clearInterval(blinkInterval);
  }, [state]);

  // Viewport clamp utility
  const clampPosition = useCallback((pos: PetPosition): PetPosition => {
    if (typeof window === 'undefined') return pos;
    const maxX = Math.max(PADDING, window.innerWidth - PET_WIDTH - PADDING);
    const maxY = Math.max(PADDING, window.innerHeight - PET_HEIGHT - PADDING);
    return {
      x: Math.min(Math.max(PADDING, pos.x), maxX),
      y: Math.min(Math.max(PADDING, pos.y), maxY),
    };
  }, []);

  // Inactivity Sleep Timer (enters sleep after 60s of stillness)
  const resetSleepTimer = useCallback(() => {
    if (sleepTimerRef.current) {
      clearTimeout(sleepTimerRef.current);
    }
    if (state === 'sleeping') {
      stateMachine.transitionTo('idle');
    }
    sleepTimerRef.current = setTimeout(() => {
      if (stateMachine.getState() === 'idle') {
        stateMachine.transitionTo('sleeping');
      }
    }, 60000);
  }, [state, stateMachine]);

  // Autonomous movement scheduler
  useEffect(() => {
    if (!settings.enabled || !settings.autonomousMovement || isReducedMotion || isDragging || state === 'sleeping') {
      if (movementTimerRef.current) clearTimeout(movementTimerRef.current);
      return;
    }

    const scheduleNextMove = () => {
      // Cadence based on intensity
      const baseDelay = settings.intensity === 'high' ? 7000 : settings.intensity === 'low' ? 22000 : 14000;
      const delay = baseDelay + Math.random() * 6000;

      movementTimerRef.current = setTimeout(() => {
        if (stateMachine.getState() !== 'idle' && stateMachine.getState() !== 'floating') {
          scheduleNextMove();
          return;
        }

        // Decide destination: random roaming, target inspection, or resting return
        const roll = Math.random();
        let targetPos: PetPosition;

        const randomTarget = petTargetRegistry.getRandomTarget();
        if (roll < 0.35 && randomTarget && randomTarget.element) {
          // Move near an active registered UI target
          const rect = randomTarget.element.getBoundingClientRect();
          targetPos = clampPosition({
            x: rect.right + 12,
            y: rect.top + (rect.height / 2) - (PET_HEIGHT / 2),
          });
        } else if (roll < 0.65 && settings.restingPosition) {
          // Return near preferred resting position
          targetPos = clampPosition(settings.restingPosition);
        } else {
          // Random calm roam inside safe quadrant
          const maxX = window.innerWidth - PET_WIDTH - PADDING;
          const maxY = window.innerHeight - PET_HEIGHT - PADDING;
          targetPos = clampPosition({
            x: PADDING + Math.random() * (maxX - PADDING),
            y: PADDING + Math.random() * (maxY - PADDING),
          });
        }

        // Update look direction based on delta X
        setPosition((currentPos) => {
          if (targetPos.x < currentPos.x - 20) {
            setLookDirection('left');
          } else if (targetPos.x > currentPos.x + 20) {
            setLookDirection('right');
          } else {
            setLookDirection('center');
          }
          return targetPos;
        });

        scheduleNextMove();
      }, delay);
    };

    scheduleNextMove();

    return () => {
      if (movementTimerRef.current) clearTimeout(movementTimerRef.current);
    };
  }, [settings.enabled, settings.autonomousMovement, settings.intensity, settings.restingPosition, isReducedMotion, isDragging, state, stateMachine, clampPosition]);

  // Pointer drag events
  const handlePointerDown = (e: React.PointerEvent) => {
    if (e.button !== 0) return; // Left click only
    setIsDragging(true);
    hasMovedSinceDragRef.current = false;
    dragStartRef.current = {
      pointerX: e.clientX,
      pointerY: e.clientY,
      petX: position.x,
      petY: position.y,
    };
    try {
      (e.target as HTMLElement).setPointerCapture(e.pointerId);
    } catch {
      // Safe fallback for testing environments without pointer capture
    }
    resetSleepTimer();
    if (settings.soundEnabled) petAudio.playChirp('click');
  };

  const handlePointerMove = (e: React.PointerEvent) => {
    if (!isDragging || !dragStartRef.current) return;
    const deltaX = e.clientX - dragStartRef.current.pointerX;
    const deltaY = e.clientY - dragStartRef.current.pointerY;

    if (Math.abs(deltaX) > 4 || Math.abs(deltaY) > 4) {
      hasMovedSinceDragRef.current = true;
    }

    const nextPos = clampPosition({
      x: dragStartRef.current.petX + deltaX,
      y: dragStartRef.current.petY + deltaY,
    });
    setPosition(nextPos);
  };

  const handlePointerUp = (e: React.PointerEvent) => {
    if (hasMovedSinceDragRef.current) {
      // User dragged to a new position -> persist as resting anchor
      savePetRestingPosition(position);
    }
    setIsDragging(false);
    try {
      (e.target as HTMLElement).releasePointerCapture(e.pointerId);
    } catch {
      // Ignored
    }
    dragStartRef.current = null;
  };

  const handleResetPosition = () => {
    const defaultPos = {
      x: window.innerWidth - PET_WIDTH - PADDING - 30,
      y: window.innerHeight - PET_HEIGHT - PADDING - 30,
    };
    const clamped = clampPosition(defaultPos);
    setPosition(clamped);
    savePetRestingPosition(clamped);
  };

  if (!settings.enabled) {
    return null;
  }

  return (
    <div className="marshal-pet-overlay">
      <div
        ref={containerRef}
        className={`marshal-pet-container state-${state} ${isDragging ? 'dragging' : ''} ${isReducedMotion ? 'reduced-motion' : ''}`}
        style={{
          transform: `translate3d(${position.x}px, ${position.y}px, 0)`,
        }}
        onPointerDown={handlePointerDown}
        onPointerMove={handlePointerMove}
        onPointerUp={handlePointerUp}
        onClick={() => {
          if (!hasMovedSinceDragRef.current) {
            setIsPopoverOpen((prev) => !prev);
            if (speech) {
              eventBridge.dismissSpeech();
            }
          }
        }}
        role="button"
        tabIndex={0}
        aria-label={`MARSHAL Assistant Mascot (${state})`}
        onKeyDown={(e) => {
          if (e.key === 'Enter' || e.key === ' ') {
            e.preventDefault();
            setIsPopoverOpen((prev) => !prev);
          }
        }}
      >
        <div className="marshal-pet-body-wrapper">
          <PetRenderer
            state={state}
            lookDirection={lookDirection}
            isBlinking={isBlinking}
          />
        </div>

        {/* Contextual Speech Bubble */}
        {!isPopoverOpen && (
          <PetSpeechBubble
            message={speech}
            onDismiss={() => eventBridge.dismissSpeech()}
            onAction={onNavigate}
          />
        )}

        {/* Click Popover Menu */}
        <PetPopover
          isOpen={isPopoverOpen}
          onClose={() => setIsPopoverOpen(false)}
          state={state}
          onStateChange={(s) => stateMachine.transitionTo(s)}
          onNavigate={onNavigate}
          onResetPosition={handleResetPosition}
        />
      </div>

      {/* Dev-only Playground Panel */}
      <PetDevTools
        bridge={eventBridge}
        onStateChange={(s) => stateMachine.transitionTo(s)}
      />
    </div>
  );
}
