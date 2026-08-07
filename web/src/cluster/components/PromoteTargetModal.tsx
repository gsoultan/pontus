import { Badge, Button, Group, Modal, Radio, Stack, Text } from '@mantine/core'
import { IconServerOff } from '@tabler/icons-react'
import { useState } from 'react'
import type { BackendStatus } from '../../gen/api/proto/domain/status_pb'
import { EmptyState } from '../../common/components/EmptyState'

interface PromoteTargetModalProps {
  opened: boolean
  onClose: () => void
  replicas: BackendStatus[]
  onSelect: (address: string) => void
}

function lagTone(lagMs: number): { color: string; label: string } {
  if (lagMs <= 0) return { color: 'successGreen', label: 'in sync' }
  if (lagMs < 1000) return { color: 'successGreen', label: `${lagMs}ms behind` }
  if (lagMs < 10_000) return { color: 'orange', label: `${(lagMs / 1000).toFixed(1)}s behind` }
  return { color: 'red', label: `${(lagMs / 1000).toFixed(0)}s behind` }
}

/**
 * Picks the promotion target. Replication lag is shown per candidate because
 * it is the amount of data that will be lost when the write path moves — an
 * operator should not have to guess which replica is furthest ahead.
 */
export function PromoteTargetModal({
  opened,
  onClose,
  replicas,
  onSelect,
}: PromoteTargetModalProps) {
  const [selected, setSelected] = useState<string | null>(null)

  return (
    <Modal opened={opened} onClose={onClose} title="Choose a promotion target" size="md">
      {replicas.length === 0 ? (
        <EmptyState
          icon={IconServerOff}
          title="No replicas registered"
          description="Provision or discover a replica before attempting a failover."
          compact
        />
      ) : (
        <Stack gap="md">
          <Text size="sm" c="dimmed">
            The replica with the least lag will lose the least data when it takes over.
          </Text>

          <Radio.Group value={selected} onChange={setSelected}>
            <Stack gap="xs">
              {replicas.map((replica) => {
                const lag = lagTone(Number(replica.replicationLagMs))
                return (
                  <Radio.Card key={replica.address} value={replica.address} p="sm" radius="md">
                    <Group wrap="nowrap" align="flex-start" gap="sm">
                      <Radio.Indicator />
                      <Stack gap={4} style={{ flex: 1, minWidth: 0 }}>
                        <Group justify="space-between" wrap="nowrap">
                          <Text fw={600} size="sm" truncate>
                            {replica.address}
                          </Text>
                          <Badge size="xs" variant="light" color={lag.color}>
                            {lag.label}
                          </Badge>
                        </Group>
                        <Group gap={6}>
                          <Badge
                            size="xs"
                            variant="dot"
                            color={replica.healthy ? 'successGreen' : 'red'}
                          >
                            {replica.healthy ? 'Healthy' : 'Unhealthy'}
                          </Badge>
                          {replica.zone && (
                            <Badge size="xs" variant="default">
                              {replica.zone}
                            </Badge>
                          )}
                          {replica.isDraining && (
                            <Badge size="xs" variant="light" color="orange">
                              Draining
                            </Badge>
                          )}
                        </Group>
                      </Stack>
                    </Group>
                  </Radio.Card>
                )
              })}
            </Stack>
          </Radio.Group>

          <Group justify="flex-end" gap="sm">
            <Button variant="default" onClick={onClose}>
              Cancel
            </Button>
            <Button
              color="red"
              disabled={!selected}
              onClick={() => selected && onSelect(selected)}
            >
              Continue
            </Button>
          </Group>
        </Stack>
      )}
    </Modal>
  )
}
