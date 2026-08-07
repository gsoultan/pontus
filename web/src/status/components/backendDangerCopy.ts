import type { DangerKind } from '../hooks/useBackendActions'

export interface DangerCopy {
  title: string
  description: string
  consequences: string[]
  confirmLabel: string
  /** Present only for actions that cannot be undone from the dashboard. */
  confirmationText?: string
  severity: 'danger' | 'warning'
}

/**
 * Blast radius per destructive action. Restarting a service comes back on its
 * own; removing a backend or stopping a database does not — only the latter
 * two demand a typed confirmation.
 */
export function dangerCopy(kind: DangerKind, address: string): DangerCopy {
  switch (kind) {
    case 'remove':
      return {
        title: 'Remove backend',
        description: `${address} will be deregistered from this proxy.`,
        consequences: [
          'New connections stop being routed to this node immediately.',
          'Pooled connections to it are closed.',
          'The database itself keeps running — only Pontus forgets about it.',
        ],
        confirmLabel: 'Remove backend',
        confirmationText: address,
        severity: 'danger',
      }
    case 'shutdown':
      return {
        title: 'Shut down database',
        description: `The PostgreSQL process on ${address} will be stopped through its agent.`,
        consequences: [
          'Every session on this node is terminated.',
          'If this is the primary, writes fail until a replica is promoted.',
          'The host must be started again out of band — Pontus cannot restart a stopped database.',
        ],
        confirmLabel: 'Shut down database',
        confirmationText: address,
        severity: 'danger',
      }
    case 'restart':
      return {
        title: 'Restart database service',
        description: `The PostgreSQL service on ${address} will be restarted through its agent.`,
        consequences: [
          'Open sessions on this node are dropped.',
          'The node is unavailable for a few seconds while it comes back.',
          'Pontus routes around it until health checks pass again.',
        ],
        confirmLabel: 'Restart service',
        severity: 'warning',
      }
  }
}
