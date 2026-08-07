import { ActionIcon, Badge, Group, Progress, Stack, Table, Text, Tooltip, rem } from '@mantine/core'
import { IconPlugConnectedX } from '@tabler/icons-react'
import type { ReplicationStream } from '../../gen/api/proto/domain/status_pb'

interface StreamsTableProps {
  streams: ReplicationStream[]
  onTerminate: (stream: ReplicationStream) => void
  isAdmin: boolean
}

/** Lag is the amount of change the consumer has not acknowledged yet. */
function lagTone(lagMs: number): { color: string; label: string } {
  if (lagMs <= 0) return { color: 'successGreen', label: 'caught up' }
  if (lagMs < 5_000) return { color: 'successGreen', label: `${lagMs}ms behind` }
  if (lagMs < 60_000) return { color: 'orange', label: `${(lagMs / 1000).toFixed(1)}s behind` }
  return { color: 'red', label: `${Math.round(lagMs / 60_000)}m behind` }
}

function formatBytes(bytes: number): string {
  if (bytes <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.min(units.length - 1, Math.floor(Math.log(bytes) / Math.log(1024)))
  return `${(bytes / 1024 ** i).toFixed(i === 0 ? 0 : 1)} ${units[i]}`
}

function formatDuration(startedMs: number): string {
  if (!startedMs) return '—'
  const seconds = Math.max(0, Math.floor((Date.now() - startedMs) / 1000))
  const d = Math.floor(seconds / 86400)
  const h = Math.floor((seconds % 86400) / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  if (d > 0) return `${d}d ${h}h`
  if (h > 0) return `${h}h ${m}m`
  return `${m}m`
}

export function StreamsTable({ streams, onTerminate, isAdmin }: StreamsTableProps) {
  return (
    <Table.ScrollContainer minWidth={900}>
      <Table verticalSpacing="sm" horizontalSpacing="md">
        <Table.Thead>
          <Table.Tr>
            <Table.Th>Slot</Table.Th>
            <Table.Th>Consumer</Table.Th>
            <Table.Th>Pinned to</Table.Th>
            <Table.Th>Kind</Table.Th>
            <Table.Th w={200}>Lag</Table.Th>
            <Table.Th w={110}>Connected</Table.Th>
            {isAdmin && <Table.Th w={70} ta="center">Action</Table.Th>}
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {streams.map((stream) => {
            const lag = lagTone(Number(stream.lagMs))
            const startedMs = stream.startedAt ? Number(stream.startedAt.seconds) * 1000 : 0
            return (
              <Table.Tr key={stream.id}>
                <Table.Td>
                  <Stack gap={0}>
                    <Text size="sm" fw={600} ff="monospace">
                      {stream.slotName || <Text span c="dimmed">unattached</Text>}
                    </Text>
                    {stream.confirmedLsn && (
                      <Text size="10px" c="dimmed" ff="monospace">
                        LSN {stream.confirmedLsn}
                      </Text>
                    )}
                  </Stack>
                </Table.Td>
                <Table.Td>
                  <Stack gap={0}>
                    <Text size="sm">{stream.clientAddr}</Text>
                    <Text size="10px" c="dimmed">
                      {stream.user}
                      {stream.database ? ` · ${stream.database}` : ''}
                    </Text>
                  </Stack>
                </Table.Td>
                <Table.Td>
                  {/* Named "pinned to", not "backend" — the point is that it cannot move. */}
                  <Badge variant="light" color="grape" size="sm">
                    {stream.backendAddr}
                  </Badge>
                </Table.Td>
                <Table.Td>
                  <Stack gap={0}>
                    <Text size="sm" tt="capitalize">
                      {stream.kind || 'logical'}
                    </Text>
                    {stream.plugin && (
                      <Text size="10px" c="dimmed">
                        {stream.plugin}
                      </Text>
                    )}
                  </Stack>
                </Table.Td>
                <Table.Td>
                  <Stack gap={4}>
                    <Group justify="space-between" gap="xs">
                      <Text size="xs" fw={700} c={lag.color}>
                        {lag.label}
                      </Text>
                      <Text size="10px" c="dimmed">
                        {formatBytes(Number(stream.lagBytes))} WAL
                      </Text>
                    </Group>
                    <Progress
                      value={Math.min(100, Number(stream.lagMs) / 600)}
                      color={lag.color}
                      size="xs"
                      radius="xl"
                    />
                  </Stack>
                </Table.Td>
                <Table.Td>
                  <Text size="sm">{formatDuration(startedMs)}</Text>
                </Table.Td>
                {isAdmin && (
                  <Table.Td>
                    <Group justify="center">
                      <Tooltip label="Terminate stream">
                        <ActionIcon
                          variant="light"
                          color="red"
                          onClick={() => onTerminate(stream)}
                          aria-label={`Terminate stream ${stream.slotName || stream.id}`}
                        >
                          <IconPlugConnectedX size={16} />
                        </ActionIcon>
                      </Tooltip>
                    </Group>
                  </Table.Td>
                )}
              </Table.Tr>
            )
          })}
        </Table.Tbody>
      </Table>
      <Text size="xs" c="dimmed" p="sm" style={{ borderTop: `${rem(1)} solid light-dark(var(--mantine-color-gray-2), var(--mantine-color-dark-6))` }}>
        Streams are pinned to one node and are not load balanced. They do not survive a failover.
      </Text>
    </Table.ScrollContainer>
  )
}
