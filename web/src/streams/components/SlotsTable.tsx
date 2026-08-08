import { Alert, Badge, Group, Stack, Table, Text } from '@mantine/core'
import { IconAlertTriangle } from '@tabler/icons-react'
import type { ReplicationSlot } from '../../gen/api/proto/domain/status_pb'

interface SlotsTableProps {
  slots: ReplicationSlot[]
}

/** Retained WAL is what fills the disk when a consumer stops acknowledging. */
const RETAINED_WARN_BYTES = 1024 * 1024 * 1024 // 1 GB

function formatBytes(bytes: number): string {
  if (bytes <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.min(units.length - 1, Math.floor(Math.log(bytes) / Math.log(1024)))
  return `${(bytes / 1024 ** i).toFixed(i === 0 ? 0 : 1)} ${units[i]}`
}

/**
 * Slots read from the primary.
 *
 * Shown apart from streams rather than joined to them: PostgreSQL sees the
 * proxy's address in pg_stat_replication, not the consumer's, so attributing a
 * slot to a stream would be a confident guess. Slots are authoritative about
 * retained WAL; the streams table is authoritative about who is connected.
 */
export function SlotsTable({ slots }: SlotsTableProps) {
  const abandoned = slots.filter((s) => !s.active && Number(s.retainedBytes) > RETAINED_WARN_BYTES)

  return (
    <Stack gap="sm">
      {abandoned.length > 0 && (
        <Alert color="red" variant="light" icon={<IconAlertTriangle size={16} />} radius="md">
          <Text size="sm">
            {abandoned.length} inactive slot{abandoned.length === 1 ? '' : 's'} holding more than 1 GB
            of WAL. PostgreSQL cannot reclaim it while the slot exists — drop the slot or reconnect
            its consumer before the disk fills.
          </Text>
        </Alert>
      )}

      <Table.ScrollContainer minWidth={700}>
        <Table verticalSpacing="sm" horizontalSpacing="md">
          <Table.Thead>
            <Table.Tr>
              <Table.Th>Slot</Table.Th>
              <Table.Th>Type</Table.Th>
              <Table.Th>Database</Table.Th>
              <Table.Th w={110}>State</Table.Th>
              <Table.Th w={140}>Retained WAL</Table.Th>
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {slots.map((slot) => {
              const retained = Number(slot.retainedBytes)
              const heavy = retained > RETAINED_WARN_BYTES
              return (
                <Table.Tr key={slot.name}>
                  <Table.Td>
                    <Stack gap={0}>
                      <Text size="sm" fw={600} ff="monospace">
                        {slot.name}
                      </Text>
                      {slot.confirmedLsn && (
                        <Text size="10px" c="dimmed" ff="monospace">
                          LSN {slot.confirmedLsn}
                        </Text>
                      )}
                    </Stack>
                  </Table.Td>
                  <Table.Td>
                    <Group gap={4}>
                      <Text size="sm" tt="capitalize">
                        {slot.slotType || 'logical'}
                      </Text>
                      {slot.plugin && (
                        <Text size="10px" c="dimmed">
                          {slot.plugin}
                        </Text>
                      )}
                    </Group>
                  </Table.Td>
                  <Table.Td>
                    <Text size="sm">{slot.database || '—'}</Text>
                  </Table.Td>
                  <Table.Td>
                    <Badge size="sm" variant="dot" color={slot.active ? 'successGreen' : 'orange'}>
                      {slot.active ? 'Active' : 'Inactive'}
                    </Badge>
                  </Table.Td>
                  <Table.Td>
                    <Text size="sm" fw={heavy ? 700 : 500} c={heavy ? 'red' : undefined}>
                      {formatBytes(retained)}
                    </Text>
                  </Table.Td>
                </Table.Tr>
              )
            })}
          </Table.Tbody>
        </Table>
      </Table.ScrollContainer>
    </Stack>
  )
}
