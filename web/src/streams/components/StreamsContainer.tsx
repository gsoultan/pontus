import { useState } from 'react'
import { Alert, Badge, Card, Group, Progress, SimpleGrid, Skeleton, Stack, Text } from '@mantine/core'
import { IconAlertTriangle, IconPlugConnected } from '@tabler/icons-react'
import type { ReplicationStream } from '../../gen/api/proto/domain/status_pb'
import { EmptyState } from '../../common/components/EmptyState'
import { ConfirmDangerModal } from '../../common/components/ConfirmDangerModal'
import { useAuthStore } from '../../store/useAuthStore'
import { useStatusContext } from '../../status/hooks/useStatusContext'
import { useReplicationStreams } from '../hooks/useReplicationStreams'
import { useTerminateStream } from '../hooks/useTerminateStream'
import { ConnectConsumerCard } from './ConnectConsumerCard'
import { StreamsTable } from './StreamsTable'

export function StreamsContainer() {
  const isAdmin = useAuthStore((state) => state.role === 'admin')
  const { proxy } = useStatusContext()
  const { data, isLoading, error } = useReplicationStreams()
  const { terminateStream, isTerminating } = useTerminateStream()
  const [target, setTarget] = useState<ReplicationStream | null>(null)

  const streams = data?.streams ?? []
  const used = data?.used ?? 0
  const budget = data?.budget ?? 0
  const usage = budget > 0 ? (used / budget) * 100 : 0

  const handleTerminate = async () => {
    if (!target) return
    try {
      await terminateStream({ streamId: target.id })
      setTarget(null)
    } catch {
      // surfaced by the hook's notification
    }
  }

  if (isLoading) {
    return (
      <Stack gap="md">
        <Skeleton height={120} radius="md" />
        <Skeleton height={280} radius="md" />
      </Stack>
    )
  }

  return (
    <Stack gap="md">
      {error && (
        <Alert color="red" variant="light" icon={<IconAlertTriangle size={16} />} radius="md">
          {error.message}
        </Alert>
      )}

      <SimpleGrid cols={{ base: 1, lg: 2 }} spacing="md">
        <Card>
          <Stack gap="sm">
            <Group justify="space-between">
              <Text fw={700}>Replication budget</Text>
              <Badge variant="light" color={usage >= 100 ? 'red' : usage > 75 ? 'orange' : 'grape'}>
                {used} of {budget || '—'}
              </Badge>
            </Group>
            <Progress
              value={usage}
              color={usage >= 100 ? 'red' : usage > 75 ? 'orange' : 'grape'}
              size="md"
              radius="xl"
            />
            {/* State the consequence, not just the number. */}
            <Text size="sm" c="dimmed">
              Capacity reserved for CDC consumers. When it is full, new consumers are refused —
              your application is never affected.
            </Text>
            {usage >= 100 && (
              <Alert color="red" variant="light" radius="md">
                The budget is full. The next consumer to connect will be refused. Raise it in
                Settings or terminate a stream that is no longer needed.
              </Alert>
            )}
          </Stack>
        </Card>

        <ConnectConsumerCard proxyAddress={proxy?.address} />
      </SimpleGrid>

      <Card p={0}>
        {streams.length === 0 ? (
          <EmptyState
            icon={IconPlugConnected}
            title="No replication consumers connected"
            description="CDC clients attach themselves — copy the connection string above into Debezium or pg_recvlogical and the stream will appear here."
            color="grape"
          />
        ) : (
          <StreamsTable streams={streams} onTerminate={setTarget} isAdmin={isAdmin} />
        )}
      </Card>

      {target && (
        <ConfirmDangerModal
          opened
          onClose={() => setTarget(null)}
          onConfirm={handleTerminate}
          title="Terminate replication stream"
          confirmLabel="Terminate stream"
          confirmationText={target.slotName || target.id}
          loading={isTerminating}
          description={`This disconnects the consumer on ${target.clientAddr} reading from ${target.backendAddr}.`}
          consequences={[
            'The consumer resumes from its own checkpoint — Pontus cannot resume it.',
            'Changes produced while it is disconnected stay in WAL, and the slot holds disk until it reconnects.',
            'If the consumer does not reconnect, that WAL is never released.',
          ]}
        />
      )}
    </Stack>
  )
}
