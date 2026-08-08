import { Modal } from '@mantine/core'
import { useStatus } from '../hooks/useStatus'
import { useStatusContext } from '../hooks/useStatusContext'
import { useBackendActions } from '../hooks/useBackendActions'
import { useAuthStore } from '../../store/useAuthStore'
import { ConfirmDangerModal } from '../../common/components/ConfirmDangerModal'
import { PromoteBackendModal } from '../../cluster/components/PromoteBackendModal'
import { BackendsSection } from './BackendsSection'
import { TrafficDistribution } from './TrafficDistribution'
import { BackendModal } from './BackendModal'
import { MaintenanceModal } from './MaintenanceModal'
import { PostgresInsights } from './PostgresInsights'
import { StatusPageShell } from './StatusPageShell'
import { dangerCopy } from './backendDangerCopy'

export function BackendsView() {
  const { data } = useStatus()
  const { selectedProjectId, selectedProxyId } = useStatusContext()
  const isAdmin = useAuthStore((state) => state.role === 'admin')
  const actions = useBackendActions()

  const backends = data?.backends ?? []
  const hasPrimary = backends.some((backend) => backend.role === 'primary')
  const currentPrimary = backends.find((backend) => backend.role === 'primary')?.address
  const copy = actions.danger ? dangerCopy(actions.danger.kind, actions.danger.address) : null

  return (
    <StatusPageShell
      title="Backend Management"
      description="Register, monitor and operate the database nodes behind this proxy"
    >
      <TrafficDistribution
        backends={backends}
        balancerType={data?.balancerType}
        localZone={data?.localZone}
      />

      <BackendsSection
        backends={backends}
        onAdd={actions.openAdd}
        onEdit={actions.setEditing}
        onRemove={(address) => actions.requestDanger('remove', address)}
        onBackup={(address) => actions.openMaintenance('backup', address)}
        onRestore={(address) => actions.openMaintenance('restore', address)}
        onVacuum={(address) => actions.openMaintenance('vacuum', address)}
        onRestart={(address) => actions.requestDanger('restart', address)}
        onShutdown={(address) => actions.requestDanger('shutdown', address)}
        onPromote={actions.requestPromote}
        onShowInsights={actions.showInsights}
        isAdmin={isAdmin}
      />

      <BackendModal
        opened={actions.addOpen}
        onClose={actions.closeAdd}
        onSubmit={actions.submitAdd}
        initialValues={actions.addDefaults}
        title="Register New Backend Node"
        loading={actions.isAdding}
        hasPrimary={hasPrimary}
      />

      <BackendModal
        opened={actions.editing !== null}
        onClose={() => actions.setEditing(null)}
        onSubmit={actions.submitUpdate}
        initialValues={
          actions.editing
            ? {
                address: actions.editing.address,
                role: actions.editing.role,
                weight: actions.editing.weight,
                managedByAgent: actions.editing.managedByAgent,
                agentAddress: actions.editing.agentAddress,
                agentConfig: actions.editing.agentConfig,
              }
            : undefined
        }
        title="Edit Backend Configuration"
        loading={actions.isUpdating}
        hasPrimary={hasPrimary}
      />

      <MaintenanceModal
        opened={actions.maintenance !== null}
        onClose={actions.closeMaintenance}
        backends={backends}
        type={actions.maintenance?.kind ?? 'backup'}
        onBackup={actions.backupBackend}
        onRestore={actions.restoreBackend}
        onVacuum={actions.vacuumBackend}
        loading={actions.isMaintenanceRunning}
        initialAddress={actions.maintenance?.address ?? ''}
      />

      {copy && (
        <ConfirmDangerModal
          opened
          onClose={actions.dismissDanger}
          onConfirm={actions.confirmDanger}
          title={copy.title}
          description={copy.description}
          consequences={copy.consequences}
          confirmLabel={copy.confirmLabel}
          confirmationText={copy.confirmationText}
          severity={copy.severity}
          loading={actions.dangerPending}
        />
      )}

      <PromoteBackendModal
        opened={actions.promoteAddress !== null}
        onClose={actions.dismissPromote}
        address={actions.promoteAddress ?? ''}
        currentPrimary={currentPrimary}
      />

      <Modal
        opened={actions.insightsAddress !== null}
        onClose={actions.hideInsights}
        size="xl"
        radius="md"
        title={`Database Insights: ${actions.insightsAddress ?? ''}`}
      >
        {actions.insightsAddress && (
          <PostgresInsights
            projectId={selectedProjectId ?? ''}
            proxyId={selectedProxyId ?? ''}
            address={actions.insightsAddress}
          />
        )}
      </Modal>
    </StatusPageShell>
  )
}
