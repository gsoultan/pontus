import { useCallback, useState } from 'react'
import { useBackendManagement } from './useBackendManagement'
import type { BackendStatus } from '../../gen/api/proto/domain/status_pb'
import type { BackendConfig } from '../../gen/api/proto/domain/project_pb'

export type MaintenanceKind = 'backup' | 'restore' | 'vacuum'
export type DangerKind = 'remove' | 'restart' | 'shutdown'

export interface BackendFormValues {
  address: string
  role: string
  weight: number
}

/**
 * Owns backend mutations and the modal state that guards them.
 *
 * Destructive operations no longer call `window.confirm` — each one raises a
 * typed request that the view renders as a danger modal, so the operator can
 * see which node they are about to take offline before they agree to it.
 */
export function useBackendActions() {
  const {
    addBackend,
    updateBackend,
    removeBackend,
    isAdding,
    isUpdating,
    isRemoving,
    backupBackend,
    restoreBackend,
    vacuumBackend,
    isBackingUp,
    isRestoring,
    isVacuuming,
    restartBackend,
    shutdownBackend,
    isRestarting,
    isShuttingDown,
  } = useBackendManagement()

  const [addOpen, setAddOpen] = useState(false)
  const [addDefaults, setAddDefaults] = useState<BackendFormValues | undefined>()
  const [editing, setEditing] = useState<BackendStatus | null>(null)
  const [maintenance, setMaintenance] = useState<{ kind: MaintenanceKind; address: string } | null>(
    null,
  )
  const [danger, setDanger] = useState<{ kind: DangerKind; address: string } | null>(null)
  const [insightsAddress, setInsightsAddress] = useState<string | null>(null)
  const [promoteAddress, setPromoteAddress] = useState<string | null>(null)

  const openAdd = useCallback((role = 'primary') => {
    setAddDefaults({ address: '', role, weight: role === 'primary' ? 100 : 50 })
    setAddOpen(true)
  }, [])

  const closeAdd = useCallback(() => {
    setAddOpen(false)
    setAddDefaults(undefined)
  }, [])

  const submitAdd = useCallback(
    async (values: BackendFormValues) => {
      await addBackend({ config: values as BackendConfig })
      closeAdd()
    },
    [addBackend, closeAdd],
  )

  const submitUpdate = useCallback(
    async (values: BackendFormValues) => {
      await updateBackend({ config: values as BackendConfig })
      setEditing(null)
    },
    [updateBackend],
  )

  const confirmDanger = useCallback(async () => {
    if (!danger) return
    const { kind, address } = danger
    try {
      if (kind === 'remove') await removeBackend({ address })
      if (kind === 'restart') await restartBackend({ address })
      if (kind === 'shutdown') await shutdownBackend({ address })
      setDanger(null)
    } catch {
      // surfaced by the mutation's notification; keep the modal open
    }
  }, [danger, removeBackend, restartBackend, shutdownBackend])

  const dangerPending =
    (danger?.kind === 'remove' && isRemoving) ||
    (danger?.kind === 'restart' && isRestarting) ||
    (danger?.kind === 'shutdown' && isShuttingDown)

  return {
    // add / edit
    addOpen,
    addDefaults,
    openAdd,
    closeAdd,
    submitAdd,
    isAdding,
    editing,
    setEditing,
    submitUpdate,
    isUpdating,

    // maintenance
    maintenance,
    openMaintenance: (kind: MaintenanceKind, address: string) => setMaintenance({ kind, address }),
    closeMaintenance: () => setMaintenance(null),
    backupBackend,
    restoreBackend,
    vacuumBackend,
    isMaintenanceRunning: isBackingUp || isRestoring || isVacuuming,

    // destructive
    danger,
    requestDanger: (kind: DangerKind, address: string) => setDanger({ kind, address }),
    dismissDanger: () => setDanger(null),
    confirmDanger,
    dangerPending: Boolean(dangerPending),

    // panels
    insightsAddress,
    showInsights: setInsightsAddress,
    hideInsights: () => setInsightsAddress(null),
    promoteAddress,
    requestPromote: setPromoteAddress,
    dismissPromote: () => setPromoteAddress(null),
  }
}
