import { Text } from '@mantine/core'
import { ConfirmDangerModal } from '../../common/components/ConfirmDangerModal'
import { usePromoteBackend } from '../hooks/usePromoteBackend'

interface PromoteBackendModalProps {
  opened: boolean
  onClose: () => void
  address: string
  currentPrimary?: string
}

/**
 * Manual failover. Promotion moves the write path to another node, so it is
 * gated behind typing the target address — a misclick here takes writes away
 * from a healthy primary.
 */
export function PromoteBackendModal({
  opened,
  onClose,
  address,
  currentPrimary,
}: PromoteBackendModalProps) {
  const { promoteBackend, isPromoting } = usePromoteBackend()

  const handleConfirm = async () => {
    try {
      await promoteBackend({ address })
      onClose()
    } catch {
      // surfaced by the hook's notification
    }
  }

  return (
    <ConfirmDangerModal
      opened={opened}
      onClose={onClose}
      onConfirm={handleConfirm}
      title="Promote replica to primary"
      confirmLabel="Promote node"
      confirmationText={address}
      loading={isPromoting}
      description={
        <>
          <Text span size="sm">
            This promotes{' '}
          </Text>
          <Text span size="sm" fw={700}>
            {address}
          </Text>
          <Text span size="sm">
            {' '}
            to primary and routes all writes to it.
          </Text>
        </>
      }
      consequences={[
        currentPrimary
          ? `${currentPrimary} stops accepting writes and must be rebuilt or re-attached as a replica.`
          : 'The existing primary stops accepting writes.',
        'In-flight write transactions on the old primary will fail.',
        'Replicas still following the old primary need to be re-pointed.',
        'This cannot be undone from the dashboard.',
      ]}
    />
  )
}
