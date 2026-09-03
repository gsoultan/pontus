import { useMemo, useState } from 'react'
import {
  ActionIcon,
  Alert,
  Badge,
  Card,
  Group,
  Select,
  Text,
  TextInput,
  Tooltip,
} from '@mantine/core'
import {
  IconAlertTriangle,
  IconPlayerPause,
  IconPlayerPlay,
  IconSearch,
  IconTrash,
} from '@tabler/icons-react'
import { useDebouncedValue } from '@mantine/hooks'
import { useLogStream } from '../hooks/useLogStream'
import { LogTable, type LogRow } from './LogTable'

const LEVELS = [
  { value: 'DEBUG', label: 'Debug and above' },
  { value: 'INFO', label: 'Info and above' },
  { value: 'WARN', label: 'Warnings and above' },
  { value: 'ERROR', label: 'Errors only' },
]

export function LogsContainer() {
  const [minLevel, setMinLevel] = useState('INFO')
  const [search, setSearch] = useState('')
  const [paused, setPaused] = useState(false)
  const [debouncedSearch] = useDebouncedValue(search, 200)

  const { page, connected, error, clear } = useLogStream({
    minLevel,
    search: debouncedSearch,
    paused,
  })

  const rows = useMemo<LogRow[]>(
    () =>
      page.records.map((record) => ({
        key: String(record.seq),
        timestampMs: record.timestampMs,
        level: record.level,
        message: record.message,
        attributes: record.attributes,
      })),
    [page.records],
  )

  return (
    <Card mt="md" p={0}>
      <Group justify="space-between" p="sm" wrap="wrap" gap="sm">
        <Group gap="sm" wrap="nowrap">
          <Badge
            variant="dot"
            color={error ? 'red' : connected ? 'successGreen' : 'gray'}
            size="sm"
          >
            {error ? 'Disconnected' : connected ? 'Streaming' : 'Connecting'}
          </Badge>
          <Text size="xs" c="dimmed" fw={500}>
            {page.matched.toLocaleString()} shown · {page.buffered.toLocaleString()} buffered
            {page.dropped > 0 && ` · ${page.dropped.toLocaleString()} evicted`}
          </Text>
        </Group>

        <Group gap="xs" wrap="nowrap">
          <TextInput
            size="xs"
            radius="md"
            placeholder="Filter messages and attributes"
            leftSection={<IconSearch size={14} />}
            value={search}
            onChange={(event) => setSearch(event.currentTarget.value)}
            w={{ base: 160, sm: 240 }}
          />
          <Select
            size="xs"
            radius="md"
            data={LEVELS}
            value={minLevel}
            onChange={(value) => setMinLevel(value ?? 'INFO')}
            allowDeselect={false}
            w={170}
          />
          <Tooltip label={paused ? 'Resume capture' : 'Pause capture'}>
            <ActionIcon
              variant="light"
              color={paused ? 'successGreen' : 'gray'}
              size="lg"
              onClick={() => setPaused((value) => !value)}
              aria-label={paused ? 'Resume capture' : 'Pause capture'}
            >
              {paused ? <IconPlayerPlay size={16} /> : <IconPlayerPause size={16} />}
            </ActionIcon>
          </Tooltip>
          <Tooltip label="Clear buffer">
            <ActionIcon variant="light" color="gray" size="lg" onClick={clear} aria-label="Clear buffer">
              <IconTrash size={16} />
            </ActionIcon>
          </Tooltip>
        </Group>
      </Group>

      {error && (
        <Alert
          color="red"
          variant="light"
          radius={0}
          icon={<IconAlertTriangle size={16} />}
          title="Log stream interrupted"
        >
          {error}
        </Alert>
      )}

      {paused && (
        <Alert color="orange" variant="light" radius={0}>
          Capture paused — incoming lines are discarded until you resume.
        </Alert>
      )}

      <LogTable
        rows={rows}
        followTail
        emptyTitle={search ? 'No lines match this filter' : 'Waiting for log output'}
        emptyDescription={
          search
            ? 'Adjust the search text or lower the minimum level.'
            : 'Lines appear here as soon as the proxy emits them.'
        }
      />
    </Card>
  )
}
