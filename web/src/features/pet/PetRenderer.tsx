import type { MarshalPetState } from './types';

interface PetRendererProps {
  state: MarshalPetState;
  lookDirection?: 'left' | 'right' | 'center';
  isBlinking?: boolean;
}

export function PetRenderer({ state, lookDirection = 'center', isBlinking = false }: PetRendererProps) {
  const getEyeExpression = () => {
    if (isBlinking && state !== 'sleeping' && state !== 'error') {
      return (
        <g stroke="#38bdf8" strokeWidth="2.5" strokeLinecap="round">
          <line x1="20" y1="28" x2="28" y2="28" />
          <line x1="36" y1="28" x2="44" y2="28" />
        </g>
      );
    }

    switch (state) {
      case 'sleeping':
        return (
          <g stroke="#818cf8" strokeWidth="2.5" strokeLinecap="round" opacity="0.8">
            <path d="M 20 30 Q 24 33 28 30" fill="none" />
            <path d="M 36 30 Q 40 33 44 30" fill="none" />
            <text x="46" y="20" fill="#a5b4fc" fontSize="8" fontWeight="bold" fontFamily="sans-serif">z</text>
            <text x="51" y="14" fill="#c7d2fe" fontSize="10" fontWeight="bold" fontFamily="sans-serif">Z</text>
          </g>
        );

      case 'success':
        return (
          <g stroke="#22c55e" strokeWidth="3" strokeLinecap="round" className="pet-eye-glow">
            <path d="M 19 30 Q 24 24 29 30" fill="none" />
            <path d="M 35 30 Q 40 24 45 30" fill="none" />
            {/* Cheerful sparkle */}
            <circle cx="15" cy="20" r="1.5" fill="#4ade80" />
            <circle cx="49" cy="20" r="1.5" fill="#4ade80" />
          </g>
        );

      case 'warning':
        return (
          <g className="pet-eye-glow">
            <ellipse cx="24" cy="28" rx="4.5" ry="6" fill="#eab308" />
            <ellipse cx="40" cy="28" rx="4.5" ry="6" fill="#eab308" />
            <circle cx="24" cy="28" r="2" fill="#713f12" />
            <circle cx="40" cy="28" r="2" fill="#713f12" />
          </g>
        );

      case 'error':
        return (
          <g stroke="#ef4444" strokeWidth="2.5" strokeLinecap="round" className="pet-eye-glow">
            <line x1="20" y1="24" x2="28" y2="32" />
            <line x1="28" y1="24" x2="20" y2="32" />
            <line x1="36" y1="24" x2="44" y2="32" />
            <line x1="44" y1="24" x2="36" y2="32" />
          </g>
        );

      case 'working':
        return (
          <g className="pet-eye-glow">
            <rect x="18" y="24" width="28" height="8" rx="4" fill="#0284c7" />
            <rect x="22" y="26" width="20" height="4" rx="2" fill="#38bdf8" />
            <line x1="20" y1="28" x2="44" y2="28" stroke="#e0f2fe" strokeWidth="1.5" strokeDasharray="3 3" />
          </g>
        );

      case 'thinking':
        return (
          <g className="pet-eye-glow">
            <circle cx="22" cy="28" r="3.5" fill="#38bdf8" />
            <circle cx="32" cy="28" r="4.5" fill="#6366f1" />
            <circle cx="42" cy="28" r="3.5" fill="#38bdf8" />
          </g>
        );

      case 'reading':
        return (
          <g stroke="#38bdf8" strokeWidth="2" fill="none" className="pet-eye-glow">
            <circle cx="23" cy="29" r="4" />
            <circle cx="41" cy="29" r="4" />
            <line x1="27" y1="29" x2="37" y2="29" />
          </g>
        );

      case 'talking':
        return (
          <g className="pet-eye-glow">
            <ellipse cx="24" cy="26" rx="4" ry="5" fill="#38bdf8" />
            <ellipse cx="40" cy="26" rx="4" ry="5" fill="#38bdf8" />
            <path d="M 28 34 Q 32 37 36 34" stroke="#38bdf8" strokeWidth="2" strokeLinecap="round" fill="none" />
          </g>
        );

      case 'idle':
      case 'walking':
      case 'floating':
      default: {
        const eyeOffset = lookDirection === 'left' ? -2.5 : lookDirection === 'right' ? 2.5 : 0;
        return (
          <g className="pet-eye-glow">
            <ellipse cx={24 + eyeOffset} cy="28" rx="4" ry="5" fill="#38bdf8" />
            <ellipse cx={40 + eyeOffset} cy="28" rx="4" ry="5" fill="#38bdf8" />
            <circle cx={25 + eyeOffset} cy="26" r="1.5" fill="#ffffff" />
            <circle cx={41 + eyeOffset} cy="26" r="1.5" fill="#ffffff" />
          </g>
        );
      }
    }
  };

  const getBeaconColor = () => {
    switch (state) {
      case 'error': return '#ef4444';
      case 'warning': return '#eab308';
      case 'success': return '#22c55e';
      case 'working': return '#38bdf8';
      case 'sleeping': return '#6366f1';
      default: return '#818cf8';
    }
  };

  const getThrusterGlow = () => {
    if (state === 'sleeping') return 'rgba(99, 102, 241, 0.2)';
    if (state === 'error') return 'rgba(239, 68, 68, 0.6)';
    if (state === 'warning') return 'rgba(234, 179, 8, 0.6)';
    if (state === 'success') return 'rgba(34, 197, 94, 0.6)';
    return 'rgba(56, 189, 248, 0.6)';
  };

  return (
    <svg
      className="marshal-pet-svg"
      viewBox="0 0 64 72"
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
      aria-hidden="true"
    >
      <defs>
        {/* Chassis Gradient */}
        <linearGradient id="petChassisGrad" x1="10" y1="12" x2="54" y2="58" gradientUnits="userSpaceOnUse">
          <stop offset="0%" stopColor="#25293d" />
          <stop offset="50%" stopColor="#181b29" />
          <stop offset="100%" stopColor="#0f111a" />
        </linearGradient>

        {/* Screen Bevel Gradient */}
        <linearGradient id="petScreenGrad" x1="16" y1="18" x2="48" y2="40" gradientUnits="userSpaceOnUse">
          <stop offset="0%" stopColor="#0a0c14" />
          <stop offset="100%" stopColor="#141724" />
        </linearGradient>

        {/* Accent Glow Filter */}
        <filter id="petGlow" x="-20%" y="-20%" width="140%" height="140%">
          <feGaussianBlur stdDeviation="2" result="blur" />
          <feComposite in="SourceGraphic" in2="blur" operator="over" />
        </filter>
      </defs>

      {/* Thruster Plasma Flame */}
      <g className="pet-thruster-flame">
        <ellipse cx="32" cy="62" rx="6" ry="7" fill={getThrusterGlow()} filter="url(#petGlow)" />
        <ellipse cx="32" cy="60" rx="3.5" ry="4" fill="#ffffff" opacity="0.8" />
      </g>

      {/* Antenna & Beacon */}
      <line x1="32" y1="12" x2="32" y2="5" stroke="#474d6b" strokeWidth="2.5" strokeLinecap="round" />
      <circle cx="32" cy="4" r="3" fill={getBeaconColor()} className="pet-antenna-beacon" filter="url(#petGlow)" />

      {/* Left & Right Hover Stabilizer Fins */}
      <path d="M 8 36 C 5 32, 6 24, 11 26 L 14 29 Z" fill="#2d3148" stroke="#474d6b" strokeWidth="1" />
      <path d="M 56 36 C 59 32, 58 24, 53 26 L 50 29 Z" fill="#2d3148" stroke="#474d6b" strokeWidth="1" />

      {/* Main Robot Body Chassis */}
      <rect
        x="12"
        y="12"
        width="40"
        height="44"
        rx="14"
        fill="url(#petChassisGrad)"
        stroke="#404666"
        strokeWidth="1.5"
      />

      {/* Digital Face Screen */}
      <rect
        x="16"
        y="18"
        width="32"
        height="22"
        rx="6"
        fill="url(#petScreenGrad)"
        stroke="#2e344e"
        strokeWidth="1"
      />

      {/* Dynamic Eye Expressions */}
      {getEyeExpression()}

      {/* MARSHAL Chest Badge (M monogram) */}
      <g transform="translate(24, 43)">
        <rect x="0" y="0" width="16" height="8" rx="3" fill="#131622" stroke="#38bdf8" strokeWidth="0.8" opacity="0.9" />
        {/* 'M' Monogram */}
        <path
          d="M 4 6.5 L 4 2.5 L 8 5 L 12 2.5 L 12 6.5"
          fill="none"
          stroke="#38bdf8"
          strokeWidth="1.2"
          strokeLinecap="round"
          strokeLinejoin="round"
        />
      </g>

      {/* Edge Accent Trim Lines */}
      <path d="M 16 52 Q 32 55 48 52" stroke="#38bdf8" strokeWidth="1" strokeLinecap="round" opacity="0.4" />
    </svg>
  );
}
