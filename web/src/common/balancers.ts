/**
 * The balancing strategies Pontus actually implements.
 *
 * One list, imported everywhere a strategy is chosen. It used to be written out
 * per screen, and the copies drifted: Settings offered "Random", which has no
 * implementation at all, and the proxy-creation form offered `least_conn`,
 * which the server did not match — so choosing "Least Connections" there
 * silently ran round-robin.
 *
 * Values must match newBalancer() in server/management/infrastructure/registry.
 */
export const BALANCERS = [
  { value: 'round-robin', label: 'Round Robin' },
  { value: 'weighted-round-robin', label: 'Weighted Round Robin' },
  { value: 'least-conns', label: 'Least Connections' },
  { value: 'p2c', label: 'Power of Two Choices' },
  { value: 'peak-ewma', label: 'Peak EWMA (latency-aware)' },
  { value: 'consistent', label: 'Consistent Hash' },
] as const

export const DEFAULT_BALANCER = 'round-robin'

/** Human label for a stored value, including the older underscore spellings. */
export function balancerLabel(value: string): string {
  const normalized = value.trim().toLowerCase().replace(/_/g, '-')
  const match = BALANCERS.find((b) => b.value === normalized)
  if (match) return match.label

  // Aliases the server accepts but which are not offered as choices.
  if (normalized === 'ip-hash') return 'Consistent Hash'
  if (normalized === 'least-connections' || normalized === 'leastconn') return 'Least Connections'
  if (normalized === 'ewma') return 'Peak EWMA (latency-aware)'
  return value || 'Round Robin'
}
