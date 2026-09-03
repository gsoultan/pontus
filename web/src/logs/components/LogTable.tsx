import { Box, Code, Group, ScrollArea, Stack, Text, rem } from '@mantine/core'
import { IconFileSearch } from '@tabler/icons-react'
import { EmptyState } from '../../common/components/EmptyState'
import { LogLevelBadge } from './LogLevelBadge'

export interface LogRow {
  key: string
  timestampMs: number
  level: string
  message: string
  attributes: Record<string, string>
}

interface LogTableProps {
  rows: LogRow[]
  height?: number
  emptyTitle?: string
  emptyDescription?: string
  /** Pins the viewport to the newest row — used by the live stream. */
  followTail?: boolean
}

function formatTime(ms: number): string {
  const date = new Date(ms)
  const time = date.toLocaleTimeString(undefined, { hour12: false })
  return `${time}.${String(date.getMilliseconds()).padStart(3, '0')}`
}

/**
 * Renders log lines as escaped JSX text. Log messages and their attributes are
 * attacker-influenced (they can carry client-supplied SQL and identifiers), so
 * nothing here may ever become HTML or markdown.
 */
export function LogTable({
  rows,
  height = 560,
  emptyTitle = 'No log entries',
  emptyDescription,
  followTail = false,
}: LogTableProps) {
  if (rows.length === 0) {
    return <EmptyState icon={IconFileSearch} title={emptyTitle} description={emptyDescription} />
  }

  return (
    <ScrollArea
      h={height}
      type="auto"
      offsetScrollbars
      styles={{ viewport: { scrollBehavior: followTail ? 'smooth' : 'auto' } }}
    >
      <Stack gap={0}>
        {rows.map((row) => (
          <Box
            key={row.key}
            px="sm"
            py={6}
            style={{
              borderBottom: `${rem(1)} solid light-dark(var(--mantine-color-gray-1), var(--mantine-color-dark-6))`,
            }}
          >
            <Group gap="sm" wrap="nowrap" align="flex-start">
              <Text
                size="xs"
                c="dimmed"
                ff="monospace"
                style={{ fontVariantNumeric: 'tabular-nums', whiteSpace: 'nowrap' }}
                mt={2}
              >
                {formatTime(row.timestampMs)}
              </Text>
              <Box w={rem(64)} style={{ flexShrink: 0 }} mt={1}>
                <LogLevelBadge level={row.level} />
              </Box>
              <Box style={{ flex: 1, minWidth: 0 }}>
                <Text
                  size="sm"
                  ff="monospace"
                  style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}
                >
                  {row.message}
                </Text>
                {Object.keys(row.attributes).length > 0 && (
                  <Group gap={6} mt={4}>
                    {Object.entries(row.attributes).map(([key, value]) => (
                      <Code key={key} style={{ fontSize: rem(11) }}>
                        {key}={value}
                      </Code>
                    ))}
                  </Group>
                )}
              </Box>
            </Group>
          </Box>
        ))}
      </Stack>
    </ScrollArea>
  )
}
