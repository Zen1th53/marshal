import type { AuthUser } from './AuthContext';

export function hasAuthority(user: AuthUser | null, authority: string): boolean {
  if (!user) return false;
  if (user.role === 'admin') return true;
  return user.authorities.includes(authority) || user.authorities.includes('*');
}

export function canMutateState(user: AuthUser | null): boolean {
  if (!user) return false;
  return user.role !== 'viewer';
}
