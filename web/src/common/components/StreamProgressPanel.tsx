import { Alert, Badge, Box, Code, Group, Progress, ScrollArea, Stack, Text, rem } from '@mantine/core'
import { IconAlertTriangle, IconCircleCheck } from '@tabler/icons-react'
import type { ProgressEvent } from '../hooks/useStreamedOperation'

interface StreamProgressPanelProps {
  events: ProgressEvent[]
  percentage: number
  stage: string
  running: boolean
  finished: boolean
  error: string | null
}

/**
 * Renders the live transcript of a streaming operation. Stage messages come
 * from the agent host, so they are rendered as escaped text only.
 */
export function StreamProgressPanel({
  events,
  percentage,
  stage,
  running,
  finished,
  error,
}: StreamProgressPanelProps) {
  if (events.length === 0 && !error && !running) return null

  return (
    <Stack gap="sm">
      <Group justify="space-between" gap="sm">
        <Group gap="xs">
          {finished && !error && (
            <Badge color="successGreen" variant="light" leftSection={<IconCircleCheck size={12} />}>
              Complete
            </Badge>
          )}
          {running && (
            <Badge color="pontusBlue" variant="light">
              {stage || 'Working'}
            </Badge>
          )}
          {error && (
            <Badge color="red" variant="light" leftSection={<IconAlertTriangle size={12} />}>
              Failed
            </Badge>
          )}
        </Group>
        <Text size="sm" fw={700} style={{ fontVariantNumeric: 'tabular-nums' }}>
          {percentage}%
        </Text>
      </Group>

      <Progress
        value={error ? 100 : percentage}
        color={error ? 'red' : finished ? 'successGreen' : 'pontusBlue'}
        animated={running}
        striped={running}
        radius="xl"
      />

      {events.length > 0 && (
        <ScrollArea h={180} type="auto" offsetScrollbars>
          <Stack gap={2}>
            {events.map((event, index) => (
              <Group key={index} gap="xs" wrap="nowrap" align="flex-start">
                <Box w={rem(72)} style={{ flexShrink: 0 }}>
                  <Text size="xs" c="dimmed" fw={600} truncate>
                    {event.stage}
                  </Text>
                </Box>
                <Code style={{ fontSize: rem(11), background: 'transparent', padding: 0 }}>
                  {event.message}
                </Code>
              </Group>
            ))}
          </Stack>
        </ScrollArea>
      )}

      {error && (
        <Alert color="red" variant="light" icon={<IconAlertTriangle size={16} />} radius="md">
          {error}
        </Alert>
      )}
    </Stack>
  )
}
