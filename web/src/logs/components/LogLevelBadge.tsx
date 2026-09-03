import { Badge } from '@mantine/core'

const LEVEL_COLOR: Record<string, string> = {
  DEBUG: 'gray',
  INFO: 'pontusBlue',
  WARN: 'orange',
  WARNING: 'orange',
  ERROR: 'red',
  FATAL: 'red',
}

interface LogLevelBadgeProps {
  level: string
}

export function LogLevelBadge({ level }: LogLevelBadgeProps) {
  const normalized = level.toUpperCase()
  return (
    <Badge
      size="xs"
      radius="sm"
      variant="light"
      color={LEVEL_COLOR[normalized] ?? 'gray'}
      style={{ fontVariantNumeric: 'tabular-nums' }}
    >
      {normalized || 'INFO'}
    </Badge>
  )
}
