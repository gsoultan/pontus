import { Button, Paper, Stack, Text, ThemeIcon } from '@mantine/core'
import type { Icon } from '@tabler/icons-react'
import type { ReactNode } from 'react'

interface EmptyStateProps {
  icon: Icon
  title: string
  description?: string
  action?: ReactNode
  onRetry?: () => void
  color?: string
  compact?: boolean
}

/**
 * The single empty/idle placeholder. Every list, table and panel uses this so
 * "nothing here" reads the same everywhere instead of being reinvented per view.
 */
export function EmptyState({
  icon: Icon,
  title,
  description,
  action,
  onRetry,
  color = 'gray',
  compact = false,
}: EmptyStateProps) {
  return (
    <Paper withBorder radius="md" p={compact ? 'lg' : 'xl'}>
      <Stack align="center" gap="xs">
        <ThemeIcon variant="light" color={color} size={compact ? 'lg' : 'xl'} radius="xl">
          <Icon size={compact ? 20 : 24} stroke={1.5} />
        </ThemeIcon>
        <Text fw={700} c={color === 'gray' ? undefined : color}>
          {title}
        </Text>
        {description && (
          <Text size="sm" c="dimmed" ta="center" maw={420}>
            {description}
          </Text>
        )}
        {action}
        {onRetry && (
          <Button variant="light" size="xs" mt="xs" onClick={onRetry}>
            Retry
          </Button>
        )}
      </Stack>
    </Paper>
  )
}
