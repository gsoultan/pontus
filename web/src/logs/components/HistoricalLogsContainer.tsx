import { useMemo, useState } from 'react'
import {
  Alert,
  Card,
  Group,
  Loader,
  Pagination,
  Select,
  Skeleton,
  Stack,
  Text,
  TextInput,
} from '@mantine/core'
import { IconAlertTriangle, IconSearch } from '@tabler/icons-react'
import { useDebouncedValue } from '@mantine/hooks'
import { useHistoricalLogs } from '../hooks/useHistoricalLogs'
import { LogTable, type LogRow } from './LogTable'

const PAGE_SIZE = 100

const LEVELS = [
  { value: 'ALL', label: 'All levels' },
  { value: 'DEBUG', label: 'Debug' },
  { value: 'INFO', label: 'Info' },
  { value: 'WARN', label: 'Warning' },
  { value: 'ERROR', label: 'Error' },
]

const RANGES = [
  { value: '1', label: 'Last hour' },
  { value: '6', label: 'Last 6 hours' },
  { value: '24', label: 'Last 24 hours' },
  { value: '72', label: 'Last 3 days' },
  { value: '168', label: 'Last 7 days' },
]

export function HistoricalLogsContainer() {
  const [rangeHours, setRangeHours] = useState('24')
  const [level, setLevel] = useState('ALL')
  const [search, setSearch] = useState('')
  const [page, setPage] = useState(0)
  const [debouncedSearch] = useDebouncedValue(search, 300)

  const { data, isLoading, isFetching, error, refetch } = useHistoricalLogs({
    rangeHours: Number(rangeHours),
    level,
    search: debouncedSearch,
    page,
    pageSize: PAGE_SIZE,
  })

  const rows = useMemo<LogRow[]>(
    () =>
      (data?.logs ?? []).map((entry, index) => ({
        key: `${page}-${index}`,
        timestampMs: entry.timestamp ? Number(entry.timestamp.seconds) * 1000 : 0,
        level: entry.level || 'INFO',
        message: entry.message,
        attributes: entry.attributes ?? {},
      })),
    [data?.logs, page],
  )

  const total = data?.totalCount ?? 0
  const pageCount = Math.max(1, Math.ceil(total / PAGE_SIZE))

  const resetTo = (apply: () => void) => {
    apply()
    setPage(0)
  }

  return (
    <Card mt="md" p={0}>
      <Group justify="space-between" p="sm" wrap="wrap" gap="sm">
        <Group gap="xs" wrap="nowrap">
          <Text size="xs" c="dimmed" fw={500}>
            {isLoading ? 'Searching…' : `${total.toLocaleString()} entries`}
          </Text>
          {isFetching && !isLoading && <Loader size="xs" />}
        </Group>

        <Group gap="xs" wrap="nowrap">
          <TextInput
            size="xs"
            radius="md"
            placeholder="Search message text"
            leftSection={<IconSearch size={14} />}
            value={search}
            onChange={(event) => resetTo(() => setSearch(event.currentTarget.value))}
            w={{ base: 160, sm: 240 }}
          />
          <Select
            size="xs"
            radius="md"
            data={LEVELS}
            value={level}
            onChange={(value) => resetTo(() => setLevel(value ?? 'ALL'))}
            allowDeselect={false}
            w={130}
          />
          <Select
            size="xs"
            radius="md"
            data={RANGES}
            value={rangeHours}
            onChange={(value) => resetTo(() => setRangeHours(value ?? '24'))}
            allowDeselect={false}
            w={140}
          />
        </Group>
      </Group>

      {error && (
        <Alert
          color="red"
          variant="light"
          radius={0}
          icon={<IconAlertTriangle size={16} />}
          title="Could not load logs"
          withCloseButton
          onClose={() => void refetch()}
        >
          {error.message}
        </Alert>
      )}

      {isLoading ? (
        <Stack gap="xs" p="sm">
          {Array.from({ length: 8 }, (_, index) => (
            <Skeleton key={index} height={28} radius="sm" />
          ))}
        </Stack>
      ) : (
        <LogTable
          rows={rows}
          emptyTitle="No entries in this window"
          emptyDescription="Widen the time range, lower the level, or clear the search."
        />
      )}

      {pageCount > 1 && (
        <Group justify="center" p="sm">
          <Pagination
            size="sm"
            radius="md"
            total={pageCount}
            value={page + 1}
            onChange={(value) => setPage(value - 1)}
            withEdges
          />
        </Group>
      )}
    </Card>
  )
}
