import { useMemo, useState } from 'react'
import { Badge, Button, Card, Group, SimpleGrid, Stack, Text, ThemeIcon } from '@mantine/core'
import {
  IconArrowUpCircle,
  IconCopyPlus,
  IconServerBolt,
  IconTopologyStar3,
} from '@tabler/icons-react'
import type { Icon } from '@tabler/icons-react'
import { useStatus } from '../../status/hooks/useStatus'
import { useAuthStore } from '../../store/useAuthStore'
import { EmptyState } from '../../common/components/EmptyState'
import { DiscoverClusterModal } from './DiscoverClusterModal'
import { NodeBringupModal } from './NodeBringupModal'
import { PromoteBackendModal } from './PromoteBackendModal'
import { ProvisionReplicaModal } from './ProvisionReplicaModal'
import { PromoteTargetModal } from './PromoteTargetModal'

interface OperationCardProps {
  icon: Icon
  title: string
  description: string
  action: string
  onClick: () => void
  disabled?: boolean
  disabledReason?: string
  color?: string
}

function OperationCard({
  icon: Icon,
  title,
  description,
  action,
  onClick,
  disabled,
  disabledReason,
  color = 'pontusBlue',
}: OperationCardProps) {
  return (
    <Card>
      <Stack gap="sm" h="100%" justify="space-between">
        <Group gap="sm" align="flex-start" wrap="nowrap">
          <ThemeIcon variant="light" color={color} size="lg" radius="md">
            <Icon size={20} stroke={1.5} />
          </ThemeIcon>
          <Stack gap={2}>
            <Text fw={700}>{title}</Text>
            <Text size="sm" c="dimmed">
              {description}
            </Text>
          </Stack>
        </Group>
        <Stack gap={4}>
          <Button
            variant="light"
            color={color}
            onClick={onClick}
            disabled={disabled}
            fullWidth
          >
            {action}
          </Button>
          {disabled && disabledReason && (
            <Text size="xs" c="dimmed" ta="center">
              {disabledReason}
            </Text>
          )}
        </Stack>
      </Stack>
    </Card>
  )
}

/**
 * The home for cluster-shaping operations: failover, replica provisioning,
 * topology discovery and bare-host bring-up. These are all admin-only and all
 * change the shape of the cluster rather than its configuration.
 */
export function ClusterOperations() {
  const isAdmin = useAuthStore((state) => state.role === 'admin')
  const { data } = useStatus()
  const [openModal, setOpenModal] = useState<
    'promote-pick' | 'provision' | 'discover' | 'bringup' | null
  >(null)
  const [promoteTarget, setPromoteTarget] = useState<string | null>(null)

  // Derive from `data.backends` rather than a defaulted local — `?? []`
  // allocates a fresh array every render and would defeat every memo below.
  const { addresses, primaries, replicas, backendCount } = useMemo(() => {
    const list = data?.backends ?? []
    return {
      addresses: list.map((backend) => backend.address),
      primaries: list.filter((b) => b.role === 'primary').map((b) => b.address),
      replicas: list.filter((b) => b.role !== 'primary'),
      backendCount: list.length,
    }
  }, [data?.backends])

  const currentPrimary = primaries[0]

  if (!isAdmin) {
    return (
      <EmptyState
        icon={IconServerBolt}
        title="Administrator access required"
        description="Cluster operations change which node serves writes. Ask an administrator to perform them."
      />
    )
  }

  return (
    <Stack gap="md">
      <Group gap="xs">
        <Badge variant="light" color="pontusBlue">
          {backendCount} node{backendCount === 1 ? '' : 's'}
        </Badge>
        <Badge variant="light" color={currentPrimary ? 'successGreen' : 'red'}>
          {currentPrimary ? `Primary: ${currentPrimary}` : 'No primary'}
        </Badge>
        <Badge variant="light" color="gray">
          {replicas.length} replica{replicas.length === 1 ? '' : 's'}
        </Badge>
      </Group>

      <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
        <OperationCard
          icon={IconArrowUpCircle}
          title="Manual failover"
          description="Promote a replica to primary and move the write path to it."
          action="Promote a replica"
          color="red"
          onClick={() => setOpenModal('promote-pick')}
          disabled={replicas.length === 0}
          disabledReason="No replicas are registered to promote."
        />

        <OperationCard
          icon={IconCopyPlus}
          title="Provision replica"
          description="Stream a base backup from an existing node onto a new host."
          action="Provision a replica"
          onClick={() => setOpenModal('provision')}
          disabled={addresses.length === 0}
          disabledReason="Register at least one backend to copy from."
        />

        <OperationCard
          icon={IconTopologyStar3}
          title="Discover topology"
          description="Scan a primary for replicas already streaming from it and register them."
          action="Scan cluster"
          onClick={() => setOpenModal('discover')}
          disabled={primaries.length === 0}
          disabledReason="A registered primary is required to scan from."
        />

        <OperationCard
          icon={IconServerBolt}
          title="Bring up a host"
          description="Install PostgreSQL on a clean machine and initialize its data directory."
          action="Install and initialize"
          onClick={() => setOpenModal('bringup')}
        />
      </SimpleGrid>

      <PromoteTargetModal
        opened={openModal === 'promote-pick'}
        onClose={() => setOpenModal(null)}
        replicas={replicas}
        onSelect={(address) => {
          setOpenModal(null)
          setPromoteTarget(address)
        }}
      />

      <PromoteBackendModal
        opened={promoteTarget !== null}
        onClose={() => setPromoteTarget(null)}
        address={promoteTarget ?? ''}
        currentPrimary={currentPrimary}
      />

      <ProvisionReplicaModal
        opened={openModal === 'provision'}
        onClose={() => setOpenModal(null)}
        sourceOptions={addresses}
        defaultSource={currentPrimary}
      />

      <DiscoverClusterModal
        opened={openModal === 'discover'}
        onClose={() => setOpenModal(null)}
        primaryOptions={primaries}
        defaultPrimary={currentPrimary}
      />

      <NodeBringupModal opened={openModal === 'bringup'} onClose={() => setOpenModal(null)} />
    </Stack>
  )
}
